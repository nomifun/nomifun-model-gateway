// SPDX-License-Identifier: Apache-2.0
package relay

import "bytes"

// sseLineEnd recognizes SSE's CRLF, LF and CR line terminators. A CR at the
// current transport boundary waits for a possible LF; EOF resolves it as CR.
func sseLineEnd(buffer []byte, start int, eof bool) (end, next int, found bool) {
	for index := start; index < len(buffer); index++ {
		switch buffer[index] {
		case '\n':
			return index, index + 1, true
		case '\r':
			if index+1 == len(buffer) && !eof {
				return 0, 0, false
			}
			next = index + 1
			if next < len(buffer) && buffer[next] == '\n' {
				next++
			}
			return index, next, true
		}
	}
	return 0, 0, false
}

type sseRecordFramer struct{ scan, lineStart int }

// Keep scan offsets across reads so a long record arriving in tiny transport
// chunks is examined once, rather than rescanning its accumulated prefix.
func (f *sseRecordFramer) next(buffer []byte, eof bool) int {
	for f.scan < len(buffer) {
		end, next, found := sseLineEnd(buffer, f.scan, eof)
		if !found {
			f.scan = len(buffer)
			if !eof && buffer[len(buffer)-1] == '\r' {
				f.scan--
			}
			return -1
		}
		blank := end == f.lineStart
		f.scan, f.lineStart = next, next
		if blank {
			f.scan, f.lineStart = 0, 0
			return next
		}
	}
	return -1
}

func eventEnd(buffer []byte, eof bool) int {
	var framer sseRecordFramer
	return framer.next(buffer, eof)
}

// parseSSEFields reads only the event and data fields alongside an unchanged
// native frame. It also accepts a final unterminated line for the EOF sentinel
// check, without making arbitrary EOF JSON records eligible for commitment.
func parseSSEFields(frame []byte) (event string, data []byte, dataSeen bool) {
	for start := 0; start < len(frame); {
		end, next, found := sseLineEnd(frame, start, true)
		if !found {
			end, next = len(frame), len(frame)
		}
		line := frame[start:end]
		start = next
		if len(line) == 0 {
			break
		}
		if line[0] == ':' {
			continue
		}
		field, value, hasColon := bytes.Cut(line, []byte{':'})
		if !hasColon {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(field) {
		case "event":
			event = string(value)
		case "data":
			if dataSeen {
				data = append(data, '\n')
			}
			data = append(data, value...)
			dataSeen = true
		}
	}
	return event, data, dataSeen
}

func validSSEDone(event string, data []byte, dataSeen bool) bool {
	return dataSeen && bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) && (event == "" || event == "message" || event == "done")
}
