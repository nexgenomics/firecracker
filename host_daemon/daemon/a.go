package main


import (
	"fmt"
	"log"
	"ngen/config"
	"os"
	"github.com/nats-io/nats.go"
	"time"
)

var cfg struct {
	config.Ngen

	Firecracker struct {
		DataDir string `json:"data_dir"`
	} `json:"firecracker"`
}

var (
	nc *nats.Conn
)

// read_config
func read_config() error {
	cfg_paths := []string {
		os.Getenv("FIRECRACKER_CONF"),
		"/config/ngen.json",
		"./ngen.json",
	}
	return config.NewStruct(&cfg, cfg_paths)
}


// main
func main() {
	log.Printf ("FIRECRACKER DAEMON")
	log.Printf ("==================")

	if e := read_config(); e != nil {
		panic(e)
	}

	if _nc,e := nats.Connect(fmt.Sprintf("nats://%s:%d", cfg.Nats.Host, cfg.Nats.Port)); e == nil {
		nc = _nc
	} else {
		panic(e)
	}
	defer nc.Close()

	if e := perform_or_verify_installation(); e != nil {
		panic(e)
	}

	if e := start_guests(); e != nil {
		panic(e)
	}

	run_nats_loop()

	log.Printf ("***")
}



// run_nats_loop assumes that host id has already been checked and is available.
func run_nats_loop() {

	hostid,e := get_host_id()
	if e != nil {
		panic(e)
	}

	if signon,e := nc.Request ("firecracker.control-channel", []byte(fmt.Sprintf("I'm here! %s", hostid)), 5 * time.Second); e == nil {
		log.Printf ("Signed on to firecracker host controller, %s", signon)
	} else {
		log.Printf ("Failed to sign on to firecracker host controller, aborting loop, %s", e)
		return // abort the loop
	}


	subject := fmt.Sprintf ("firecracker.host.%s", hostid)
	log.Printf ("Reading NATS subject %s", subject)


	nc.Subscribe(subject, func (msg *nats.Msg) {
		log.Printf ("Received control message, reply to %s", msg.Reply)
		log.Printf ("Received control message, data %s", string(msg.Data))
		if msg.Reply != "" {
			nc.Publish (msg.Reply, []byte("Boom"))
		}
	})

	select{}
}

