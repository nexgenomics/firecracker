package main

/* This root-only process insulates the selkirk sentence stream to firecracker
 * agents, and leaves them in local files accessible to user (customer) code.
 *
 * We use an ephemeral NATS pull consumer, which is complicated and messy.
 * We do the best we can to minimize the network traffic, apply some backpressure,
 * and be resilient when the NATS connections drop.
 *
 * This thing is tricky enough that we need to be wary of it and anticipate
 * the need to fix bugs we don't know about yet.
 */

import (
	"context"
	"fmt"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	agent_id             string
	tenant_id            string = "0"
	nats_url             string = "nats://192.168.0.225:4222"
	persist_dir          string
	highest_persist_file string

	nc *nats.Conn
	js jetstream.JetStream // nats.JetStreamContext
	//subscr *nats.Subscription
	consumer jetstream.Consumer

	MAX_PERSISTS    = 100
	PERSIST_PATTERN = "as-*.bin"
)

// setup_persist_dir tries to set up a filesystem location for storing locally-persisted
// sentences. The idea is that a system daemon retrieves sentences from a nats durable stream
// and stores them on the local filesystem for retrieval by user code. This insulates the
// nats facility from user code.
// We support an env string for testing, which is usually /tmp.
// The MkdirAll call works like mkdir -p. There's no error if the directory exists,
// and an error if it can't create it.
// We create the dir with wide permissions so user code can access it.
func setup_persist_dir() error {
	persist_dir = os.Getenv("PERSIST_DIR")
	if persist_dir == "" {
		persist_dir = "/opt/agentsentences"
	}
	highest_persist_file = fmt.Sprintf("%s/highest_persisted_sequence", persist_dir)
	return os.MkdirAll(persist_dir, 0777)
}

// main
func main() {

	log.Printf("Guest Sentences")

	if e := setup_persist_dir(); e != nil {
		panic(e)
	}

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
	tick2 := time.NewTicker(10 * time.Second)
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
	filter := fmt.Sprintf("agent.sentences.%s.%s", tenant_id, agent_id)
	last_persisted := get_highest_persist()
	// Nats is persnickety about this. 0 gives an error.

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, e := js.Stream(ctx, "AGENT_SENTENCES")
	if e != nil {
		log.Printf("%v", e)
		return e
	}

	cons, e := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		DeliverPolicy:  jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:    last_persisted + 1,
		AckPolicy:      jetstream.AckExplicitPolicy,
		FilterSubjects: []string{filter},
	})
	if e != nil {
		log.Printf("%v", e)
		return e
	}

	consumer = cons
	return nil
	/*
		subscr, e = js.PullSubscribe(
			filter,
			"",
			nats.BindStream(stream),
			nats.StartSequence (last_persisted),
			nats.InactiveThreshold(20*time.Minute),
		)

		if e == nil {
			log.Printf("created pull consumer %v", subscr)
		} else {
			subscr = nil
			log.Printf("failed to create pull consumer %v", e)
		}
	*/

	return e
}

// get_a_message
func get_a_message() {
	if n := get_n_persists(); n > MAX_PERSISTS {
		log.Printf("Too many persists, %d, waiting", n)
		time.Sleep(1 * time.Second)
		return
	}

	log.Printf("get a message...")

	n_highest := get_highest_persist()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, e := consumer.Next(jetstream.FetchContext(ctx))
	if e != nil {
		log.Printf("%v", e)
		//if e != nats.ErrTimeout {
		time.Sleep(100 * time.Millisecond)
		//}
		return
	}

	m.Ack()
	meta, e := m.Metadata()
	ss := meta.Sequence.Stream

	if ss > n_highest {
		// persist and store new highest
		persist_msg(ss, m.Data())
	}

	log.Printf("%v", ss)
	/*
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
	*/
}

// get_highest_persist reads a file containing the stream-sequence number
// of the last message to be persisted. If there is no last-persisted sequence,
// the return value is 0.
func get_highest_persist() (out uint64) {
	if d, e := os.ReadFile(highest_persist_file); e == nil {
		s := strings.TrimSpace(string(d))
		out, _ = strconv.ParseUint(s, 10, 64)
	}
	return
}

// persist_msg
func persist_msg(seq uint64, data []byte) {
	seqstr := fmt.Sprintf("%020d", seq)

	fp := filepath.Join(persist_dir, fmt.Sprintf("as-%s.bin", seqstr))
	os.WriteFile(fp, data, 0600)

	os.WriteFile(highest_persist_file, []byte(seqstr), 0600)
	log.Printf("persisted %s", fp)
}

// get_n_persists counts the number of messages that we have stored locally
// and are pending processing by user code. Limiting this is a form of backpressure.
func get_n_persists() int {
	glob := filepath.Join(persist_dir, PERSIST_PATTERN)
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
		js, e = jetstream.New(nc) // nc.JetStream()
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
