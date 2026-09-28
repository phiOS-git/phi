package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// piProc is one live `pi --mode rpc` child process (binding contract §6,
// §8): JSONL commands on stdin, a `response` (correlated by id) plus a
// stream of session/extension-UI events on stdout (pi docs: rpc.md,
// json.md, rpc-extension-ui.md). This file is free of HTTP — Server
// (serve.go) owns the session registry, the SSE fan-out, and the mapping
// from pi's events to the binding contract §8 SSE shapes.
//
// Framing (rpc.md "Framing"): one JSON object per line, split ONLY on '\n',
// with an optional trailing '\r' stripped. Lines can be very long (a full
// assistant turn's tool arguments, images, …), so stdout is read with a
// bufio.Reader.ReadBytes — never a bufio.Scanner, whose default token limit
// (64KiB) would silently truncate the protocol.

// piCloseGrace is how long close() waits for an orderly exit (stdin closed)
// before killing the process (§8: "Closing stdin asks pi to shut down; kill
// after 5 s if it has not exited.").
var piCloseGrace = 5 * time.Second

// piDefaultTimeout bounds piProc.send when the caller does not specify one.
const piDefaultTimeout = 60 * time.Second

// EventFunc receives every stdout record from a piProc that is not a command
// response: session events (agent_start, message_update, …) and
// extension_ui_request, as their raw JSON plus the "type" field.
type EventFunc func(kind string, raw json.RawMessage)

// piResponse is the "response" record for one command (rpc.md).
type piResponse struct {
	Success bool
	Error   string
	Data    json.RawMessage
}

// wireRecord is the subset of fields piProc needs from any stdout line to
// route it: a command response is correlated by Type=="response" and ID;
// everything else is an event (or an extension_ui_request) forwarded as-is.
type wireRecord struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type pendingCmd struct {
	resp chan piResponse
}

// piProc wraps one `pi --mode rpc` child process.
type piProc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex

	onEvent EventFunc
	onExit  func(error) // called once, after the process has exited and every pending command has been failed

	mu      sync.Mutex
	pending map[string]pendingCmd
	nextID  int64
	closed  bool // stdin closed or process gone; send() refuses after this

	exited  chan struct{} // closed once the process has exited and onExit has run
	exitErr error
}

// startPiProc starts argv (argv[0] is the absolute launcher path, argv[1:]
// its arguments — the shape BuildLaunch returns) as a piProc. stderrSink, if
// non-nil, receives each stderr line (diagnostics only — never protocol
// data; §8 asks it be logged prefixed with the session id, which the caller
// bakes into stderrSink).
func startPiProc(argv []string, stderrSink func(line string), onEvent EventFunc, onExit func(error)) (*piProc, error) {
	if len(argv) == 0 {
		return nil, errors.New("startPiProc: empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &piProc{
		cmd:     cmd,
		stdin:   stdin,
		onEvent: onEvent,
		onExit:  onExit,
		pending: make(map[string]pendingCmd),
		exited:  make(chan struct{}),
	}

	go p.readStderr(stderr, stderrSink)
	go p.readStdout(stdout)

	return p, nil
}

// readStderr forwards each stderr line to sink. stderr is diagnostics only,
// never parsed as protocol.
func (p *piProc) readStderr(r io.Reader, sink func(line string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if sink != nil {
			sink(sc.Text())
		}
	}
}

// readStdout is the protocol reader: one JSON record per line (rpc.md),
// dispatched to a pending command's response channel or to onEvent. When
// stdout closes it waits for the process and fails every still-pending
// command before signalling exit.
func (p *piProc) readStdout(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			p.handleLine(line)
		}
		if err != nil {
			break
		}
	}

	waitErr := p.cmd.Wait()

	p.mu.Lock()
	p.closed = true
	pending := p.pending
	p.pending = nil
	p.exitErr = waitErr
	p.mu.Unlock()

	for _, pc := range pending {
		select {
		case pc.resp <- piResponse{Success: false, Error: "pi process exited"}:
		default:
		}
		close(pc.resp)
	}

	if p.onExit != nil {
		p.onExit(waitErr)
	}
	close(p.exited)
}

func (p *piProc) handleLine(line []byte) {
	var wr wireRecord
	if err := json.Unmarshal(line, &wr); err != nil {
		return // tolerate a malformed line rather than killing the reader
	}
	if wr.Type == "response" {
		p.mu.Lock()
		pc, ok := p.pending[wr.ID]
		if ok {
			delete(p.pending, wr.ID)
		}
		p.mu.Unlock()
		if ok {
			pc.resp <- piResponse{Success: wr.Success, Error: wr.Error, Data: wr.Data}
		}
		return
	}
	if p.onEvent != nil {
		p.onEvent(wr.Type, json.RawMessage(line))
	}
}

// newID mints a locally-unique command id when the caller does not supply
// one.
func (p *piProc) newID() string {
	p.mu.Lock()
	p.nextID++
	n := p.nextID
	p.mu.Unlock()
	return fmt.Sprintf("phi-%d", n)
}

// send writes one JSONL command to pi's stdin and waits for its correlated
// "response" record, up to timeout (0 = piDefaultTimeout). cmd["id"] is set
// if absent. Safe for concurrent use.
func (p *piProc) send(cmd map[string]any, timeout time.Duration) (piResponse, error) {
	if timeout <= 0 {
		timeout = piDefaultTimeout
	}
	id, _ := cmd["id"].(string)
	if id == "" {
		id = p.newID()
		cmd["id"] = id
	}

	ch := make(chan piResponse, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return piResponse{}, errors.New("piProc: process has exited")
	}
	p.pending[id] = pendingCmd{resp: ch}
	p.mu.Unlock()

	if err := p.writeLine(cmd); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return piResponse{}, err
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return piResponse{}, errors.New("piProc: process exited before responding")
		}
		return resp, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return piResponse{}, fmt.Errorf("piProc: command %q timed out after %s", cmd["type"], timeout)
	}
}

// sendRaw writes one JSONL record with no response correlation (an
// extension_ui_response, which carries its own id but is not a "command").
func (p *piProc) sendRaw(v any) error {
	return p.writeLine(v)
}

func (p *piProc) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(b)
	return err
}

// close asks pi to shut down (closing stdin) and waits for it to exit,
// killing it after piCloseGrace if it has not (§8). Safe to call more than
// once and from multiple goroutines.
func (p *piProc) close() {
	p.writeMu.Lock()
	_ = p.stdin.Close()
	p.writeMu.Unlock()

	select {
	case <-p.exited:
		return
	case <-time.After(piCloseGrace):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}
