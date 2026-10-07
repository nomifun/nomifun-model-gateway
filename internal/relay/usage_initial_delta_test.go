// SPDX-License-Identifier: Apache-2.0
package relay

import "testing"

func TestAnthropicFirstDeltaCannotEraseInitialOutputEvidence(t *testing.T) {
	p := newUsage("anthropic")
	for _, frame := range []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":9}}}` + "\n\n",
		`data: {"type":"message_delta","usage":{"output_tokens":4}}` + "\n\n",
		`data: {"type":"message_stop"}` + "\n\n",
	} {
		if _, err := p.event([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	u := p.finish(true)
	if u.Complete || u.InputTokens != 5 || u.OutputTokens != 9 {
		t.Fatalf("regression must retain initial evidence and prevent settlement: complete=%t input=%d output=%d", u.Complete, u.InputTokens, u.OutputTokens)
	}
}
