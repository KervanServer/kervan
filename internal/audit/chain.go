package audit

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Tamper-evident audit logs: every record written by a chained FileSink
// carries
//
//	"chain":{"seq":N,"prev":"<mac of record N-1>","mac":"<mac of record N>"}
//
// appended to the event object, where
//
//	mac = HMAC-SHA256(key, seq "\n" prev "\n" event-json)
//
// over the exact event bytes on disk. Editing, deleting, inserting or
// reordering records breaks the chain, and forging it requires the key.
// Removing records from the *end* of the file cannot be detected from the
// file alone: anchor the latest seq/mac elsewhere (e.g. a syslog output, or
// the output of "kervan audit verify").

const chainKeySize = 32

// chainMarker separates the event JSON from the chain object on a line. The
// Event type has no "chain" field, so the marker only occurs at this
// structural position.
var chainMarker = []byte(`,"chain":{`)

// LoadOrCreateChainKey reads the HMAC key at path, creating a random one
// (mode 0600) when the file does not exist.
func LoadOrCreateChainKey(path string) ([]byte, error) {
	// #nosec G304 -- key path is configured by trusted operators.
	raw, err := os.ReadFile(path)
	if err == nil {
		key, decErr := hex.DecodeString(string(bytes.TrimSpace(raw)))
		if decErr != nil || len(key) < chainKeySize {
			return nil, fmt.Errorf("audit integrity key %s is invalid (want %d hex-encoded bytes)", path, chainKeySize)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, chainKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	// O_EXCL: never overwrite a key another process created meanwhile.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreateChainKey(path)
		}
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, err
	}
	return key, f.Close()
}

type chainState struct {
	Seq  uint64 `json:"seq"`
	Prev string `json:"prev"`
	MAC  string `json:"mac"`
}

// chain seals successive records.
type chain struct {
	key  []byte
	seq  uint64
	prev string
}

func chainMAC(key []byte, seq uint64, prev string, event []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strconv.FormatUint(seq, 10) + "\n" + prev + "\n"))
	m.Write(event)
	return hex.EncodeToString(m.Sum(nil))
}

// seal returns the on-disk line (without newline) for an encoded event and
// the state to commit once that line is durably written.
func (c *chain) seal(event []byte) ([]byte, chainState, error) {
	event = bytes.TrimSpace(event)
	if len(event) < 2 || event[len(event)-1] != '}' {
		return nil, chainState{}, errors.New("audit event is not a JSON object")
	}
	seq := c.seq + 1
	mac := chainMAC(c.key, seq, c.prev, event)
	st := chainState{Seq: seq, Prev: c.prev, MAC: mac}
	tail, err := json.Marshal(st)
	if err != nil {
		return nil, chainState{}, err
	}
	line := make([]byte, 0, len(event)+len(tail)+12)
	line = append(line, event[:len(event)-1]...)
	line = append(line, chainMarker...)
	line = append(line, tail[1:]...) // tail without its opening brace
	line = append(line, '}')
	return line, st, nil
}

// commit advances the chain past a record that reached the file.
func (c *chain) commit(st chainState) {
	c.seq, c.prev = st.Seq, st.MAC
}

// splitChained separates a line into the original event bytes and its
// chain state; ok is false for lines without a chain.
func splitChained(line []byte) (event []byte, st chainState, ok bool, err error) {
	idx := bytes.LastIndex(line, chainMarker)
	if idx < 0 {
		return nil, st, false, nil
	}
	if line[len(line)-1] != '}' {
		return nil, st, true, errors.New("malformed chained record")
	}
	// line = event[:-1] + `,"chain":{...}` + "}"
	tail := line[idx+len(chainMarker)-1 : len(line)-1]
	if err := json.Unmarshal(tail, &st); err != nil {
		return nil, st, true, fmt.Errorf("malformed chain field: %w", err)
	}
	event = append(append([]byte{}, line[:idx]...), '}')
	return event, st, true, nil
}

// resumeChain continues the chain from the newest record of the log
// (looking past a live file that is empty right after a rotation). When that
// record is unchained or unreadable, a new segment starts.
func resumeChain(path string, key []byte) (*chain, error) {
	c := &chain{key: key}
	files, err := LogFiles(path)
	if err != nil {
		return nil, err
	}
	for i := len(files) - 1; i >= 0; i-- {
		info, err := os.Stat(files[i])
		if err != nil || info.Size() == 0 {
			continue
		}
		if st, ok := lastChainState(files[i]); ok {
			c.seq, c.prev = st.Seq, st.MAC
		}
		break
	}
	return c, nil
}

// VerifyProblem describes one integrity violation.
type VerifyProblem struct {
	Line   int    `json:"line"`
	Seq    uint64 `json:"seq,omitempty"`
	Reason string `json:"reason"`
}

// VerifyReport summarizes a log verification.
type VerifyReport struct {
	Records   int             `json:"records"`
	Chained   int             `json:"chained"`
	Unchained int             `json:"unchained"`
	Segments  int             `json:"segments"`
	LastSeq   uint64          `json:"last_seq"`
	LastMAC   string          `json:"last_mac,omitempty"`
	Problems  []VerifyProblem `json:"problems"`
}

func (r VerifyReport) OK() bool { return len(r.Problems) == 0 }

const maxVerifyProblems = 100

// VerifyChain checks every chained record in r. Unchained records (written
// before integrity was enabled) are counted but cannot be verified.
func VerifyChain(r io.Reader, key []byte) (VerifyReport, error) {
	report := VerifyReport{Problems: []VerifyProblem{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var prevSeq uint64
	prevMAC := ""
	inChain := false
	lineNo := 0
	// A segment may legitimately start mid-sequence when retention removed
	// older rotated files; that is accepted only if a sealed audit.pruned
	// record names exactly the missing predecessor (seq-1 and its MAC).
	type openStart struct {
		line int
		seq  uint64
		prev string
	}
	var starts []openStart
	pruned := map[string]bool{}
	problem := func(p VerifyProblem) {
		if len(report.Problems) < maxVerifyProblems {
			report.Problems = append(report.Problems, p)
		}
	}
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		report.Records++
		event, st, chained, err := splitChained(line)
		if !chained {
			report.Unchained++
			if inChain {
				problem(VerifyProblem{Line: lineNo, Reason: "unchained record inside a chained segment"})
			}
			inChain = false
			continue
		}
		report.Chained++
		if err != nil {
			problem(VerifyProblem{Line: lineNo, Reason: err.Error()})
			inChain = false
			continue
		}
		switch {
		case st.Seq == 1 && st.Prev == "":
			if inChain {
				// A restart right after chained records means the tail of the
				// previous segment may have been cut before a restart.
				problem(VerifyProblem{Line: lineNo, Seq: st.Seq, Reason: "chain restarted after a chained record"})
			}
			report.Segments++
		case !inChain:
			starts = append(starts, openStart{line: lineNo, seq: st.Seq, prev: st.Prev})
			report.Segments++
		case st.Seq != prevSeq+1:
			problem(VerifyProblem{Line: lineNo, Seq: st.Seq, Reason: fmt.Sprintf("sequence gap: expected %d", prevSeq+1)})
		case st.Prev != prevMAC:
			problem(VerifyProblem{Line: lineNo, Seq: st.Seq, Reason: "previous-record link mismatch (record removed or reordered)"})
		}
		macOK := hmac.Equal([]byte(chainMAC(key, st.Seq, st.Prev, event)), []byte(st.MAC))
		if !macOK {
			problem(VerifyProblem{Line: lineNo, Seq: st.Seq, Reason: "MAC mismatch (record modified, or wrong key)"})
		} else if bytes.Contains(event, []byte(`"type":"`+string(EventAuditPruned)+`"`)) {
			var evt Event
			if json.Unmarshal(event, &evt) == nil && evt.Type == EventAuditPruned {
				pruned[evt.Meta["last_seq"]+"/"+evt.Meta["last_mac"]] = true
			}
		}
		prevSeq, prevMAC, inChain = st.Seq, st.MAC, true
		report.LastSeq, report.LastMAC = st.Seq, st.MAC
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	for _, start := range starts {
		if !pruned[strconv.FormatUint(start.seq-1, 10)+"/"+start.prev] {
			problem(VerifyProblem{Line: start.line, Seq: start.seq, Reason: "chain does not start at seq 1 and no audit.pruned record explains the missing records"})
		}
	}
	return report, nil
}
