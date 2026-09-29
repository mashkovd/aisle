package agy

import (
	"errors"
	"time"
)

// Antigravity stores each step of a conversation as a protobuf message
// without a published schema. aisle reads only the few fields below, found
// by inspecting conversations it created itself and confirmed to have the
// same shape in every conversation of a real installation:
//
//	steps.step_type 14 (user input):     payload 19 → 2  the prompt
//	steps.step_type 15 (model response): payload 20 → 1  the visible reply
//	                                     (20 → 3 is the model's thinking and
//	                                     20 → 7 its tool calls; both skipped)
//	steps.metadata:                      1 → 1  created at, Unix seconds
const (
	stepUserInput     = 14
	stepModelResponse = 15
)

var errProto = errors.New("malformed protobuf")

// field returns the first length-delimited field num of msg.
func field(msg []byte, num int) ([]byte, bool, error) {
	for i := 0; i < len(msg); {
		key, n := uvarint(msg[i:])
		if n <= 0 {
			return nil, false, errProto
		}
		i += n
		f, wire := int(key>>3), key&7
		switch wire {
		case 0:
			_, n := uvarint(msg[i:])
			if n <= 0 {
				return nil, false, errProto
			}
			i += n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			l, n := uvarint(msg[i:])
			if n <= 0 || l > uint64(len(msg)-i-n) {
				return nil, false, errProto
			}
			i += n
			if f == num {
				return msg[i : i+int(l)], true, nil
			}
			i += int(l)
		default:
			return nil, false, errProto
		}
		if i > len(msg) {
			return nil, false, errProto
		}
	}
	return nil, false, nil
}

// varintField returns the first varint field num of msg.
func varintField(msg []byte, num int) (uint64, bool) {
	for i := 0; i < len(msg); {
		key, n := uvarint(msg[i:])
		if n <= 0 {
			return 0, false
		}
		i += n
		f, wire := int(key>>3), key&7
		switch wire {
		case 0:
			v, n := uvarint(msg[i:])
			if n <= 0 {
				return 0, false
			}
			if f == num {
				return v, true
			}
			i += n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			l, n := uvarint(msg[i:])
			if n <= 0 || l > uint64(len(msg)-i-n) {
				return 0, false
			}
			i += n + int(l)
		default:
			return 0, false
		}
	}
	return 0, false
}

func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// stepText returns the searchable text of a step: the prompt of a user
// input, the visible reply of a model response. ok is false for steps
// without such text (tool calls, tool output, thinking-only responses).
func stepText(stepType int, payload []byte) (role, text string, ok bool, err error) {
	outer, sub, role := 19, 2, "user"
	if stepType == stepModelResponse {
		outer, sub, role = 20, 1, "assistant"
	}
	msg, found, err := field(payload, outer)
	if err != nil || !found {
		return "", "", false, err
	}
	b, found, err := field(msg, sub)
	if err != nil || !found {
		return "", "", false, err
	}
	return role, string(b), true, nil
}

// stepTime reads the creation time from a step's metadata.
func stepTime(metadata []byte) time.Time {
	ts, found, err := field(metadata, 1)
	if err != nil || !found {
		return time.Time{}
	}
	if s, ok := varintField(ts, 1); ok {
		return time.Unix(int64(s), 0)
	}
	return time.Time{}
}
