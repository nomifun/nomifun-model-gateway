// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/nomifun/nomifun-model-gateway/conformance"
)

const help = `NomiFun Model Gateway protocol v1 external HTTP conformance suite

Usage: conformance [--help]

Configuration is read only from environment and a JSON fixture:
  NMG_BASE_URL      Gateway HTTP(S) root URL (default http://127.0.0.1:8788).
                   No user information, query string or fragment is accepted.
  NMG_API_KEY       Authorized synthetic API key (required).
  NMG_FIXTURE_FILE  JSON file selecting model IDs by endpoint type
                   (default testdata/conformance.json, the local mock fixture).

Other implementations must supply their own fixture model IDs. Optional billing
error keys, restricted-key visibility and a second authorized Responses key can
be configured there. Missing optional cases are reported as SKIP. A model fixture
is required for every advertised capability. Native request_bodies overrides
allow model-specific parameters; synthetic_assertions enables mock-only channel
and signature/tool replay assertions. No real upstream credentials are needed
for M0. Calls to a real gateway may consume its configured model quota.

Keys are never accepted as command-line arguments. Output contains only assertion
names and sanitized diagnostic text, never HTTP URLs, bodies or credentials.
Exit status: 0 when no assertion FAILs, 1 for any FAIL, 2 for configuration errors.
M0 conformance proves local protocol behavior, not production relay or billing.
`

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			fmt.Print(help)
			return 0
		}
		fmt.Fprintln(os.Stderr, "Unsupported argument. Use --help; configuration is read from environment.")
		return 2
	}
	base := os.Getenv("NMG_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:8788"
	}
	file := os.Getenv("NMG_FIXTURE_FILE")
	if file == "" {
		file = "testdata/conformance.json"
	}
	f, e := os.Open(file)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Could not open conformance fixture. Set NMG_FIXTURE_FILE or run from the repository root.")
		return 2
	}
	defer f.Close()
	fixture, e := conformance.LoadFixture(f)
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	results, e := conformance.Run(ctx, conformance.Config{BaseURL: base, APIKey: os.Getenv("NMG_API_KEY"), Fixture: fixture})
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		return 2
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
		if r.Detail == "" {
			fmt.Printf("%s %s\n", r.Status, r.Name)
		} else {
			fmt.Printf("%s %s: %s\n", r.Status, r.Name, r.Detail)
		}
	}
	fmt.Printf("Results: %d PASS, %d FAIL, %d SKIP\n", counts[conformance.Pass], counts[conformance.Fail], counts[conformance.Skip])
	if counts[conformance.Fail] > 0 {
		return 1
	}
	return 0
}
