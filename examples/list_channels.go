//go:build ignore

// Smoke test: load creds, fetch EPG, print channel list.
//
// DATASYNC_ID=... SAPISID=... SECURE_3PSID=... go run ./examples/list_channels.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ygelfand/lib-yttv/auth"
	"github.com/ygelfand/lib-yttv/epg"
	"github.com/ygelfand/lib-yttv/innertube"
)

func main() {
	creds := &auth.Creds{
		GoogleAccountID: os.Getenv("DATASYNC_ID"),
		SAPISID:         os.Getenv("SAPISID"),
		Secure3PSID:     os.Getenv("SECURE_3PSID"),
	}
	if err := creds.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "load creds:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	chs, err := epg.Fetch(ctx, innertube.New(creds))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch epg:", err)
		os.Exit(1)
	}
	for _, c := range chs {
		fmt.Printf("%-20s  %s  %s\n", c.Name, c.PerAiringVideoID, c.CurrentTitle)
	}
}
