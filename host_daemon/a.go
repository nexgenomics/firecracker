package main

/* This is a daemon for firecracker hosts.
 * It manages a local fleet of firecracker guests running agents.
 * It has to run as a systemd daemon because it needs privileged
 * access to system resources, so docker isn't a good choice.
 *
 * We manage the runtimes of firecracker guests, which are invoked
 * via the firecracker binary in conjunction with a unix-domain
 * socket.
 * This is important: WE DO NOT daemonize the guests!
 * That means the individual guests, which are controlled by a running
 * firecracker process, are and remain child processes of this daemon.
 * Damonizing would have been cleaner, but it's a pain in Go.
 * Therefore, this process needs to stay up and running. If it crashes,
 * the guests are all gone.
 */

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/nats-io/nats.go"
	"github.com/segmentio/kafka-go"
	"io"
	"log"
	"ngen/config"
	"os"
	"os/exec"
	"path/filepath"
	"sdp/datamodel"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var cfg struct {
	config.Ngen

	Firecracker struct {
		HostId            string `json:"host-id"`
		FirecrackerBinary string `json:"firecracker-binary"`
		UnixSocketPrefix  string `json:"unix-socket-prefix"`

		RootFilesystemDir string `json:"root-fs-dir"`
		VmlinuxLocation   string `json:"vmlinux-location"`
	} `json:"firecracker"`
}

var (
	pgres                    *datamodel.Session
	firecracker_log_producer *kafka.Writer
	nc                       *nats.Conn
)

// read_config
func read_config() error {
	cfg_paths := []string{
		os.Getenv("FIRECRACKER_CONF"),
		"/config/ngen.json",
		"./ngen.json",
	}
	return config.NewStruct(&cfg, cfg_paths)
}

// main
func main() {
	log.Printf("=======================")
	log.Printf("FIRECRACKER HOST DAEMON")
	log.Printf("=======================")

	if e := read_config(); e != nil {
		panic(e)
	}

	log.Printf("Firecracker host daemon id: %s", cfg.Firecracker.HostId)

	if s, e := datamodel.New(&datamodel.Config{
		Host:   cfg.Db.Host,
		Port:   cfg.Db.Port,
		User:   cfg.Db.User,
		Psw:    cfg.Db.Psw,
		Dbname: cfg.Db.Dbname,
	}); e == nil {
		pgres = s
	} else {
		panic(e)
	}

	firecracker_log_producer = &kafka.Writer{
		Addr:  kafka.TCP(cfg.Kafka.Brokers[0]),
		Topic: cfg.Kafka.FirecrackerLogTopic,
		Async: true,
	}
	defer firecracker_log_producer.Close()

	var e error
	nc, e = nats.Connect(fmt.Sprintf("nats://%s:%d", cfg.Nats.Host, cfg.Nats.Port))
	if e != nil {
		panic(e)
	}
	defer nc.Drain()

	natsCh := make(chan *nats.Msg, 64)
	sub, e := nc.ChanSubscribe(fmt.Sprintf("firecracker.host.%s", cfg.Firecracker.HostId), natsCh)
	if e != nil {
		panic(e)
	}
	defer sub.Unsubscribe()

	// This is too frequently to be hitting postgres.
	// Improve the fanout someday by caching this in a Nats subject.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	run_guest_lifecycle()

	for {
		select {
		case msg := <-natsCh:
			go process_msg(msg)
		case <-ticker.C:
			run_guest_lifecycle()
		}
	}

	log.Printf("Done")
	//run_guest_lifecycle()

}

// process_msg runs on a goroutine and receives a message from this host's NATS channel.
func process_msg(m *nats.Msg) {
	log.Printf("received %s", string(m.Data))
	if m.Reply != "" {
		nc.Publish(m.Reply, []byte("penis"))
	}
}

type lifecycle_status struct {
	Tasks         []string `json:"tasks"`
	RunningAgents []string `json:"running_agents"`
}

func new_lifecycle_status() *lifecycle_status {
	return &lifecycle_status{
		Tasks:         []string{},
		RunningAgents: []string{},
	}
}

// run_guest_lifecycle should run periodically, like every minute or so.
// It spins up or shuts down firecracker guests as specified by Postgres's firecracker_slot
// table, which is authoritative.
func run_guest_lifecycle() {

	status := new_lifecycle_status()

	// which slots are defined in the database?
	defined_slots, e := pgres.GetEnabledFirecrackerSlots(cfg.Firecracker.HostId)
	if e != nil {
		panic(e)
	}
	for _, s := range defined_slots {
		log.Printf("Slot defined: %s, %d, %v", s.Agent, s.Slot, s.Enabled)
	}

	// which slots are currently running?
	running_slots, _ := ListFirecrackerProcessesWithID()
	for _, s := range running_slots {
		agent_slot := fmt.Sprintf("%sZ%d", s.Agent, s.Slot)
		log.Print(agent_slot)
		status.RunningAgents = append(status.RunningAgents, agent_slot)
	}

	// which of the defined slots from the database are not currently running? START THEM.
	for _, m := range defined_slots {
		runs := false
		for _, n := range running_slots {
			if m.Slot == n.Slot {
				runs = true
				break
			}
		}
		if !runs {
			status_line := fmt.Sprintf("NEED TO START SLOT %d", m.Slot)
			log.Print(status_line)
			status.Tasks = append(status.Tasks, status_line)
			if e := start_vm(&m); e != nil {
				log.Printf("failed to start slot %d, %s", m.Slot, e)
			}
		}
	}

	// which of the actually running slots are not defined in the database? STOP THEM.
	for _, m := range running_slots {
		defined := false
		for _, n := range defined_slots {
			if m.Slot == n.Slot {
				defined = true
				break
			}
		}
		if !defined {
			status_line := fmt.Sprintf("NEED TO STOP SLOT %d", m.Slot)
			log.Print(status_line)
			status.Tasks = append(status.Tasks, status_line)
			if e := stop_vm(&m); e != nil {
				log.Printf("failed to stop slot %d, %s", m.Slot, e)
			}
		}
	}

	// Now write a status entry
	j, _ := json.MarshalIndent(status, "", " ")
	msg := kafka.Message{
		Key:   []byte(cfg.Firecracker.HostId),
		Value: j,
	}
	if e := firecracker_log_producer.WriteMessages(context.Background(), msg); e != nil {
		log.Printf("? %s", e)
	}
}

// stop_vm
func stop_vm(slot *FirecrackerProc) error {
	socket := fmt.Sprintf("%s.%d", cfg.Firecracker.UnixSocketPrefix, slot.Slot)
	log.Printf("stopping socket %s", socket)

	sout, serr, exitcode, e := CurlPutJSON("http://localhost/actions", socket, []byte(`{"action_type":"SendCtrlAltDel"}`))
	log.Printf(">>> %s", sout)
	log.Printf(">>> %s", serr)
	log.Printf(">>> %d", exitcode)
	log.Printf(">>> %s", e)

	time.Sleep(1 * time.Second)

	if proc, e := os.FindProcess(slot.PID); e == nil {
		e = proc.Kill()
	}

	time.Sleep(300 * time.Millisecond)
	if _, e := os.FindProcess(slot.PID); e == nil {
		log.Printf("killed firecracker pid %d, slot %d, agent %s", slot.PID, slot.Slot, slot.Agent)
	} else {
		log.Printf("FAILED to kill firecracker pid %d, slot %d, agent %s, error %s", slot.PID, slot.Slot, slot.Agent, e)
	}

	os.Remove(socket)

	return e
}

// get_or_create_rootfs is called when starting up a guest. We try to detect a root-filesystem
// for the guest. If we don't find one, try to bootstrap one from the agent-image.
// An alternate strategy would be to bootstrap EVERY TIME we come here, which would implement
// a no-persistence strategy in case of guest failures or movements of agents to another host.
//
// The config property RootFilesystemDir identifies a directory ON THIS HOST where BOTH
// agent filesystems and image filesystems are stored.
func get_or_create_rootfs(slot *datamodel.FirecrackerSlot) (string, error) {

	if cfg.Firecracker.RootFilesystemDir == "" {
		return "", fmt.Errorf("unconfigured root fs location")
	}

	// check for an agent filesystem file.
	rootfsfile := fmt.Sprintf("%s/%s.ext4", cfg.Firecracker.RootFilesystemDir, slot.Agent)
	if info, e := os.Stat(rootfsfile); e == nil {
		if info.Mode().IsRegular() {
			return rootfsfile, nil
		}
	}

	// There's no rootfilesystem. Check for the image filesystem.
	imagefsfile := fmt.Sprintf("%s/%s.ext4", cfg.Firecracker.RootFilesystemDir, slot.Image)
	if info, e := os.Stat(imagefsfile); e == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("bad image file %s", imagefsfile)
		} else {
			// this path falls through to creating an agent filesystem.
		}
	} else {
		return "", e
	}

	// Now create the agent filesystem.
	// first, copy the image file to a bogus filename.
	if e := copyFile(imagefsfile, rootfsfile); e != nil {
		return "", e
	}

	return rootfsfile, nil

	// There has been thought of mounting and personalizing the newly-cloned
	// agent image but it's a real pain in Go. So for now, we define there
	// to be no customizations required.
	// If we ever have to do it, probably use bash or python, but have it be
	// outside of this workflow.

	/*
		// next, ensure a temporary mount point.
		if e := os.MkdirAll ("/tmp/mnt", 0755); e != nil {
			return "", e
		}
		// next, mount the new file
		cmd := exec.Command("mount", "-o", "loop", "/tmp/newfile", "/tmp/mnt")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e := cmd.Run(); e != nil {
			return "",e
		}
		defer exec.Command ("umount", "/tmp/mnt")
	*/

	return "", fmt.Errorf("unim")
}

// copyFile fills the gap in the Go standard libraries. For no reason I can understand,
// they made a concious decision not to offer os.CopyFile.
func copyFile(src, dst string) error {
	// Open source file
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Create destination file (truncate if exists)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		// Ensure close and capture any close error
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()

	// Copy data
	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	return nil
}

// start_vm
func start_vm(slot *datamodel.FirecrackerSlot) error {

	rootfs_file, e := get_or_create_rootfs(slot)
	if e != nil {
		return e
	}

	api_sock := fmt.Sprintf("%s.%d", cfg.Firecracker.UnixSocketPrefix, slot.Slot)
	os.Remove(api_sock) // this is for safety in case we left a zombie on a prior run
	log.Printf("Starting agent %s, slot %d, socket %s, rootfs %s", slot.Agent, slot.Slot, api_sock, rootfs_file)

	args := []string{
		cfg.Firecracker.FirecrackerBinary,
		"--id", fmt.Sprintf("%sZ%d", slot.Agent, slot.Slot),
		"--api-sock", api_sock,
	}
	log.Printf("Invoking %s", args)

	// THIS IS GOOD: It runs the firecracker as a non-detached child process
	c := exec.Command(args[0], args[1:]...)
	c.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	//c.Stdout = os.Stdout
	//c.Stderr = os.Stderr
	c.Stdout, _ = os.Open(os.DevNull)
	c.Stderr, _ = os.Open(os.DevNull)
	c.Stdin = nil

	if e := c.Start(); e != nil {
		log.Printf("FAILED to start firecracker, %s", e)
		return e
	}
	go c.Wait() // otherwise we get zombies

	// This attempts to run the firecracker as a fully-detached daemon process.
	// But it doesn't work.
	/*
		c := exec.Command(args[0], args[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}
		c.Stdout = os.NewFile(uintptr(syscall.Stdout), os.DevNull)
		c.Stderr = os.NewFile(uintptr(syscall.Stderr), os.DevNull)
		c.Stdin = nil

		if e := c.Start(); e != nil {
			log.Printf("FAILED to start daemon firecracker, %s", e)
			return e
		}
		if e := c.Process.Release(); e != nil {
			log.Printf("FAILED to release daemon firecracker, %s", e)
			return e
		}
		log.Printf ("Started firecracker daemon, pid %d, slot %s", c.Process.Pid, slot.Slot)
	*/

	// Now start the guest inside the new firecracker
	// TODO, REPORT ERRORS OUT
	_, _, _, _ = CurlPutJSONMap("http://localhost/boot-source", api_sock, map[string]any{
		"kernel_image_path": cfg.Firecracker.VmlinuxLocation,
		"boot_args":         fmt.Sprintf("reboot=k panic=1 agent=%s tenant=0 slot=%d", slot.Agent, slot.Slot),
	})
	_, _, _, _ = CurlPutJSONMap("http://localhost/drives/rootfs", api_sock, map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   rootfs_file,
		"is_root_device": true,
		"is_read_only":   false,
	})
	_, _, _, _ = CurlPutJSONMap("http://localhost/machine-config", api_sock, map[string]any{
		"vcpu_count":        2,
		"mem_size_mib":      512,
		"smt":               false,
		"track_dirty_pages": false,
		"huge_pages":        "None",
	})
	_, _, _, _ = CurlPutJSONMap("http://localhost/network-interfaces/eth0", api_sock, map[string]any{
		"iface_id":      "eth0",
		"guest_mac":     generate_guest_mac(slot.Slot),
		"host_dev_name": fmt.Sprintf("tap%d", slot.Slot),
	})
	_, _, _, _ = CurlPutJSONMap("http://localhost/actions", api_sock, map[string]any{
		"action_type": "InstanceStart",
	})

	return nil
}

// generate_guest_mac is critical and sensitive because the guest VMs use it to
// synthesize their local IP addresses. The rule is that a MAC address of the form
// XX:YY:AA:BB:CC:DD will result in an local IP address of AA.BB.CC.DD.
// As of this writing (05Dec25), we have to generate the IP address into the space
// 10.0.0.0/16.
// What we do is map the slot number into the bottom of the guest MAC, but now we
// have the problem of avoiding invalid IP addresses where the bottom octet is
// 0 or 255.
// So here's the plan: split the slot number at decimal 100. Create the bottom
// octet by adding 100 to slot % 100. Create the penultimate octet as slot / 100.
func generate_guest_mac(slot int) string {
	var hundreds int = (slot / 100) * 256
	var hundred int = (slot % 100) + 100
	t := fmt.Sprintf("%04x", hundreds+hundred)
	mac := fmt.Sprintf("fc:fc:0a:00:%s:%s", t[0:2], t[2:4])
	return mac
}

/*
type RunningSlot struct {
	Agent string
	Slot  int
}

// discover_running_slots, DEPRECATED, prefer ListFirecrackerProcessesWithID
func discover_running_slots() []RunningSlot {

	// look for running unix-domain sockets associated with firecracker VMs.

	pattern := cfg.Firecracker.UnixSocketPrefix + ".*"
	m, e := filepath.Glob(pattern)
	if e != nil {
		panic(e)
	}

	var socketnames []string
	for _, path := range m {
		info, err := os.Stat(path)
		if err != nil {
			// Skip entries we can't stat
			continue
		}
		if info.Mode()&os.ModeSocket != 0 {
			socketnames = append(socketnames, path)
		}
	}

	sockets := []RunningSlot{}

	for _, s := range socketnames {
		ok := false
		sout, serr, exitcode, e := runCurl(s)
		if e == nil && exitcode == 0 && sout != "" {

			if agent, slot, e := decompose_firecracker_id(sout); e == nil {
				log.Printf("Slot is alive: %s, %d", agent, slot)
				ok = true
				sockets = append(sockets, RunningSlot{agent, slot})
			} else {
				log.Printf("BAD PARSE FROM RUNNING SLOT %s", sout)
			}

			if !ok {
				if err := os.Remove(s); err == nil {
					log.Printf("Removed dead socket %s (%s,%s,%d,%s)", s, sout, serr, exitcode, e)
				} else {
					log.Printf("FAILED to remove dead socket %s (%s) (%s,%s,%d,%s)", s, err, sout, serr, exitcode, e)
				}
			}

		}
	}

	return sockets
}
*/

// decompose_firecracker_id takes the returned output from a curl status-request to
// a running firecracker instance (which it returns through its unix-domain socket interface),
// and detects the presence of an id value in the output (which is in JSON).
// We generate the ID when we instantiate the firecracker binary elsewhere in this program.
//
// The id string of a firecracker process is a concatenation of the
// Agent Id (which looks like a GUID containing hyphens and alphahex characters),
// followed by an upper-case Z, and decimal digits comprising a slot number.
func decompose_firecracker_id(statusstring string) (agent string, slot int, e error) {
	t := map[string]any{}
	if e = json.Unmarshal([]byte(statusstring), &t); e != nil {
		return
	}
	id := t["id"].(string)
	idary := strings.Split(id, "Z")
	if len(idary) < 2 {
		e = fmt.Errorf("bad status string format")
		return
	}
	agent = idary[0]
	if slot, e = strconv.Atoi(idary[1]); e != nil {
		return
	}
	//log.Printf("GOLD %s, %d, %s",idary[0], slotnum, e)
	return
}

// runCurl
func runCurl(sock string) (stdout, stderr string, exitCode int, err error) {
	// Construct the command exactly as you would run it in the shell:
	cmd := exec.Command("curl", "--unix-socket", sock, "http://localhost/")

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Run the command
	err = cmd.Run()

	// Default exit code
	exitCode = 0

	// Retrieve exit status
	if err != nil {
		// If the process exited with a non-zero status,
		// err will be of type *exec.ExitError.
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		} else {
			// Some other failure (e.g., command not found)
			return outBuf.String(), errBuf.String(), exitCode, err
		}
	} else {
		// Successful exit
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			exitCode = status.ExitStatus()
		}
	}

	return outBuf.String(), errBuf.String(), exitCode, nil
}

// CurlPutJSONMap
func CurlPutJSONMap(url string, unixsocket string, data map[string]any) (string, string, int, error) {
	j, _ := json.Marshal(data)
	return CurlPutJSON(url, unixsocket, j)
}

// CurlPutJSON
func CurlPutJSON(url string, unixsocket string, jsonData []byte) (stdout, stderr string, exitCode int, err error) {
	// Build the curl command:
	// curl -X PUT -H "Content-Type: application/json" -d '<json>' <url>
	cmd := exec.Command(
		"curl",
		"--unix-socket", unixsocket,
		"-s", // silent but still show output
		"-X", "PUT",
		"-H", "Content-Type: application/json",
		"-d", string(jsonData),
		url,
	)

	// Capture stdout/stderr
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Run the command
	err = cmd.Run()

	// Default exit code
	exitCode = 0

	// Retrieve exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		} else {
			// Non-exit errors (e.g. curl not found)
			return outBuf.String(), errBuf.String(), exitCode, err
		}
	} else {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			exitCode = status.ExitStatus()
		}
	}

	return outBuf.String(), errBuf.String(), exitCode, nil
}

// FirecrackerProc represents a Firecracker process with its PID and its --id
type FirecrackerProc struct {
	PID   int
	Agent string
	Slot  int
}

// ListFirecrackerProcessesWithID returns processes named "firecracker"
// and extracts their `--id <value>` argument from /proc/<pid>/cmdline.
func ListFirecrackerProcessesWithID() ([]FirecrackerProc, error) {
	const targetName = "firecracker"
	const procDir = "/proc"

	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}

	var results []FirecrackerProc

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Only numeric directories → PIDs
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		// Read process name to filter on "firecracker"
		commPath := filepath.Join(procDir, entry.Name(), "comm")
		commBytes, err := os.ReadFile(commPath)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(commBytes))
		if name != targetName {
			continue
		}

		// Read command line (cmdline is 0-byte separated)
		cmdlinePath := filepath.Join(procDir, entry.Name(), "cmdline")
		cmdBytes, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		// Split on NUL bytes
		args := strings.Split(string(cmdBytes), "\x00")

		// Find --id <value>
		var id string
		for i := 0; i < len(args); i++ {
			if args[i] == "--id" && i+1 < len(args) {
				id = args[i+1]
				break
			}
			// also accept --id=value
			if strings.HasPrefix(args[i], "--id=") {
				id = strings.TrimPrefix(args[i], "--id=")
				break
			}
		}

		agent := ""
		slot := -1
		id_ary := strings.Split(id, "Z")
		if len(id_ary) == 2 {
			agent = id_ary[0]
			slot, _ = strconv.Atoi(id_ary[1])
		}

		results = append(results, FirecrackerProc{
			PID:   pid,
			Agent: agent,
			Slot:  slot,
		})
	}

	return results, nil
}
