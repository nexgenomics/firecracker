package main

import (
	"fmt"
	"github.com/nats-io/nats.go"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	agent_id  string
	tenant_id string = "0"
	nats_url  string = "nats://192.168.0.225:4222"

	nc     *nats.Conn
	js     nats.JetStreamContext
	subscr *nats.Subscription

	MAX_PERSISTS         = 100
	PERSIST_DIR          = "/tmp" // MUST CHANGE TO /opt or similar
	PERSIST_PATTERN      = "as-*.bin"
	HIGHEST_PERSIST_FILE = "/tmp/highest_persist"
)

// main
func main() {

	log.Printf("Guest Sentences")

	if e := read_config(); e != nil {
		log.Fatal(e)
	}

	if _, e := connect_nats(); e != nil {
		log.Fatal(e)
	}
	defer nc.Drain()

	if e := pull_subscribe(); e != nil {
		log.Fatal(e)
	}

	/*
		stream := "AGENT_SENTENCES"
		filter := fmt.Sprintf("agent.sentences.%s.%s", tenant_id, agent_id)

		sub,e := js.PullSubscribe(
			filter,
			"",
			nats.BindStream(stream),
			nats.InactiveThreshold (20 * time.Minute),
		)
		if e != nil {
			log.Fatal(e)
		}
		log.Printf ("created pull consumer %v", sub)
	*/

	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	tick2 := time.NewTicker(300 * time.Second)
	defer tick2.Stop()

	for {
		select {
		default:
			get_a_message()

		case <-tick.C:
			// Ping the connection. If we make a new one, we also resubscribe.
			new_conn, e := connect_nats()
			if e != nil {
				log.Printf("nat connect error %v", e)
			}
			if new_conn {
				if e := pull_subscribe(); e != nil {
					log.Printf("nat subscribe error %v", e)
				}
			}

		case <-tick2.C:
			// Re-subscribe. This works around possible instabilities related to
			// the subscription inactivity timer running out on us. We set it to
			// 20 minutes, but it's possible that unexpected user-code behavior
			// will fail to drain the locally persisted messages and hit the
			// inactivity timer.
			if e := pull_subscribe(); e != nil {
				log.Printf("nat subscribe error %v", e)
			}

		}
	}

	log.Printf("Done")
}

// pull_subscribe
func pull_subscribe() error {
	stream := "AGENT_SENTENCES"
	filter := fmt.Sprintf("agent.sentences.%s.%s", tenant_id, agent_id)

	var e error
	subscr, e = js.PullSubscribe(
		filter,
		"",
		nats.BindStream(stream),
		nats.InactiveThreshold(20*time.Minute),
	)

	if e == nil {
		log.Printf("created pull consumer %v", subscr)
	} else {
		subscr = nil
		log.Printf("failed to create pull consumer %v", e)
	}

	return e
}

// get_a_message
func get_a_message() {
	if n := get_n_persists(); n > MAX_PERSISTS {
		log.Printf("Too many persists, %d, waiting", n)
		time.Sleep(1 * time.Second)
		return
	}

	msgs, e := subscr.Fetch(5, nats.MaxWait(5*time.Second))
	if e != nil {
		log.Printf("%v", e)
		if e != nats.ErrTimeout {
			time.Sleep(100 * time.Millisecond)
		}
		return
	}

	n_highest := get_highest_persist()

	for _, m := range msgs {
		// get this done with minimum latency
		m.Ack()
	}

	for _, m := range msgs {
		if md, e := m.Metadata(); e == nil {
			ss := md.Sequence.Stream
			if ss > n_highest {
				// persist and store new highest
				persist_msg(ss, m.Data)
			}
		}
	}
}

// get_highest_persist reads a file containing the stream-sequence number
// of the last message to be persisted.
func get_highest_persist() (out uint64) {
	if d, e := os.ReadFile(HIGHEST_PERSIST_FILE); e == nil {
		s := strings.TrimSpace(string(d))
		out, _ = strconv.ParseUint(s, 10, 64)
	}
	return
}

// persist_msg
func persist_msg(seq uint64, data []byte) {
	seqstr := fmt.Sprintf("%020d", seq)

	fp := filepath.Join(PERSIST_DIR, fmt.Sprintf("as-%s.bin", seqstr))
	os.WriteFile(fp, data, 0600)

	os.WriteFile(HIGHEST_PERSIST_FILE, []byte(seqstr), 0600)
	log.Printf("persisted %s", fp)
}

// get_n_persists counts the number of messages that we have stored locally
// and are pending processing by user code. Limiting this is a form of backpressure.
func get_n_persists() int {
	glob := filepath.Join(PERSIST_DIR, PERSIST_PATTERN)
	if matches, e := filepath.Glob(glob); e == nil {
		return len(matches)
	} else {
		return 0
	}
}

// connect_nats checks the global variable nc for status and reconnects if necessary.
// This is important because the NATS connection has been observed to drop occasionally.
// This returns true/false to tell the caller whether a new connection was made,
// for purposes of re-establishing subscriptions, etc.
func connect_nats() (bool, error) {
	if nc == nil {
		// We come here at the beginning of the run
		log.Printf("Initial connection to %s", nats_url)
		// fall through
	} else {
		// We come here on a periodic timer.
		// nats.Conn has additional parameters that will call back on errors, but they're
		// tricky and this should be good enough.
		s := nc.Status()
		if s == nats.CONNECTED {
			// nothing to do
			return false, nil
		}
		log.Printf("Nats connection bad status %d to %s", s, nats_url)
		// fall through
	}

	var e error
	if nc, e = nats.Connect(nats_url); e == nil {
		js, e = nc.JetStream()
	}
	return true, e
}

// read_config looks for:
// - the agent id, which in a firecracker guest is passed in as a linux boot parameter;
// - the nats server, which may come in several different ways;
// - a tenant id, which is currently hardcoded to "0"
//
// This needs fallbacks for testing outside of a guest environment.
func read_config() error {

	agent_id = getCmdlineValue("agent")
	if agent_id == "" {
		// fallback?
		log.Printf("HARDCODED AGENTID")
		agent_id = "9f69f4d9-d46e-4107-83c6-32e6cb383faa"
		if agent_id == "" {
			return fmt.Errorf("no agent id")
		}
	}

	return nil
}

// getCmdlineValue
func getCmdlineValue(key string) string {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return ""
	}
	parts := strings.Fields(string(data))
	for _, p := range parts {
		if strings.HasPrefix(p, key+"=") {
			return strings.TrimPrefix(p, key+"=")
		}
	}
	return ""
}
