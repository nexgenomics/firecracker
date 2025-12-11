package main

import (
	"encoding/json"
	"fmt"
	"github.com/nats-io/nats.go"
	"log"
	"os"
	"strings"
)

var (
	cfg struct {
		Host       string `json:"host"`
		Agent      string `json:"agent"`
		Slot       int    `json:"slot"`
		NatsServer string `json:"nats-server"`
	}
)

// read_config
func read_config() error {
	w := func(f string) error {
		if d, e := os.ReadFile(f); e == nil {
			if e := json.Unmarshal(d, &cfg); e == nil {
				return nil
			} else {
				return e
			}
		} else {
			return e
		}
	}

	// production
	if e := w("/.ngen/.id"); e == nil {
		return e
	}
	// testing
	return w("./.id")
}

// main
func main() {
	log.Printf("Guest Daemon")
	log.Printf("NEXGENOMICS, Inc.")

	if e := read_config(); e != nil {
		log.Printf("config failed: %s", e)
		//os.Exit(-1)
	}

	cfg.Agent = getCmdlineValue("agent")
	if cfg.NatsServer == "" {
		cfg.NatsServer = "nats://192.168.0.225:4222"
	}

	log.Printf("%s", cfg)

	if e := read_nats(); e != nil {
		log.Printf("nats error %s", e)
	}
}

// read_nats
func read_nats() error {
	nc, e := nats.Connect(cfg.NatsServer)
	if e != nil {
		return e
	}
	defer nc.Drain()

	if _, e := nc.Subscribe(fmt.Sprintf("firecracker.agent.%s", cfg.Agent), nats_handler); e != nil {
		return e
	}

	select {}
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

// nats_handler
func nats_handler(msg *nats.Msg) {
	log.Printf("I got a rock. %s", cfg.Agent)
	msg.Respond([]byte(fmt.Sprintf("Hi! I'm %s. You said %s", cfg.Agent, string(msg.Data))))
}
