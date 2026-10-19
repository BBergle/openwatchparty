// Command owp-conform checks an OWP relay against spec/SPEC.md (`run`) and measures one under load (`load`).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		var cfg runCfg
		fs.StringVar(&cfg.url, "url", "", "WebSocket URL of a running relay, e.g. ws://127.0.0.1:8080/v1/ws")
		fs.StringVar(&cfg.relayBin, "relay-bin", "", "relay binary to start, restart and stop (instead of --url)")
		fs.Float64Var(&cfg.scale, "timer-scale", 0.1, "the relay's --timer-scale (--relay-bin starts it with this one)")
		fs.BoolVar(&cfg.trustProxy, "trust-proxy", false, "with --url: the relay runs with --trust-proxy, so the per-IP limit scenarios run")
		fs.StringVar(&cfg.ports, "ports", "18150-18199", "with --relay-bin: port range for the relays it starts")
		fs.StringVar(&cfg.only, "only", "", "run only scenarios whose name contains this")
		fs.StringVar(&cfg.examples, "examples", "", "the spec/examples directory (default: spec/examples in the working directory or the nearest one above it)")
		caFile, connect := transportFlags(fs)
		list := fs.Bool("list", false, "list the scenarios and exit")
		_ = fs.Parse(os.Args[2:])
		if *list {
			listScenarios()
			return
		}
		if (cfg.url == "") == (cfg.relayBin == "") || cfg.scale <= 0 || (cfg.relayBin != "" && (*caFile != "" || *connect != "")) {
			usage()
		}
		useTransportOrExit(cfg.url, *caFile, *connect)
		os.Exit(runSuite(cfg))
	case "load":
		fs := flag.NewFlagSet("load", flag.ExitOnError)
		var cfg loadCfg
		fs.StringVar(&cfg.url, "url", "", "relay WebSocket URL")
		fs.IntVar(&cfg.clients, "clients", 2000, "participants")
		fs.IntVar(&cfg.roomSize, "room-size", 4, "participants per room")
		fs.IntVar(&cfg.ramp, "ramp", 500, "new connections per second while ramping")
		fs.DurationVar(&cfg.duration, "duration", 60*time.Second, "steady-state measurement time")
		fs.DurationVar(&cfg.actionInterval, "action-interval", 10*time.Second, "mean time between host actions per room (0: none)")
		fs.StringVar(&cfg.pidfile, "pidfile", "", "relay pidfile on the SSH host, relative to the home directory (empty: no sampling)")
		fs.StringVar(&cfg.sshHost, "ssh", "", "SSH destination of the relay host (needed with --pidfile)")
		fs.StringVar(&cfg.sshKey, "ssh-key", "", "SSH key (default: ssh's own)")
		fs.StringVar(&cfg.label, "label", "", "label in the RESULT line")
		caFile, connect := transportFlags(fs)
		_ = fs.Parse(os.Args[2:])
		if cfg.url == "" || cfg.clients < cfg.roomSize || cfg.roomSize < 1 || cfg.ramp < 1 || (cfg.pidfile != "" && cfg.sshHost == "") {
			usage()
		}
		useTransportOrExit(cfg.url, *caFile, *connect)
		os.Exit(runLoad(cfg))
	default:
		usage()
	}
}

func transportFlags(fs *flag.FlagSet) (caFile, connect *string) {
	caFile = fs.String("ca-file", "", "with wss://: also trust the PEM root certificates in this file (a test CA, Pebble)")
	connect = fs.String("connect", "", "HOST:PORT to dial for the --url host, which stays the TLS server name and Host header")
	return caFile, connect
}

func useTransportOrExit(relayURL, caFile, connect string) {
	if err := useTransport(relayURL, caFile, connect); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  owp-conform run --relay-bin PATH [--timer-scale 0.1] [--ports 18150-18199] [--examples DIR] [--only NAME]
  owp-conform run --url ws[s]://HOST:PORT/v1/ws [--ca-file PEM] [--connect HOST:PORT] [--timer-scale 0.1]
                  [--trust-proxy] [--examples DIR] [--only NAME]
  owp-conform run --list
  owp-conform load --url ws[s]://HOST:PORT/v1/ws [--ca-file PEM] [--connect HOST:PORT] [--pidfile DIR/relay.pid]
                   [--ssh USER@HOST] [--clients 2000] [--room-size 4] [--ramp 500] [--duration 60s]
                   [--action-interval 10s] [--label X]`)
	os.Exit(2)
}
