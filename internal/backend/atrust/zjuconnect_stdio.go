package atrustbackend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/soundadam/nju-connect/internal/credential"
)

// The pinned upstream client asks for interactive factors by printing a
// prompt through the standard logger and then calling fmt.Scanln on
// os.Stdin. These are the prompts it can print during Setup.
const (
	upstreamSMSPrompt      = "Please enter the SMS verification code"
	upstreamCaptchaPrompt  = "Please enter the graph check code JSON"
	upstreamCallbackPrompt = "Please enter the callback url"
)

// The upstream password login does not fail when the gateway refuses the
// password: it logs the gateway's answer and the next step fails for an
// unrelated reason. The bridge recognizes that answer so the caller can
// report a rejected credential instead.
const (
	upstreamRequestPrefix  = "Perform "
	upstreamPasswordLogin  = "Perform POST /passport/v1/auth/psw"
	maximumRejectionDetail = 200
)

var upstreamGatewayAnswer = regexp.MustCompile(`Code: (-?\d+), Message: (.*)$`)

// stdioBridgeMu serializes bridges: os.Stdin and the standard logger are
// process-wide, so only one upstream Setup may run at a time.
var stdioBridgeMu sync.Mutex

// stdioBridge replaces os.Stdin with a pipe and watches the standard logger
// for upstream prompts, answering each one through the Prompter. The upstream
// client therefore never reads the real terminal, and the host keeps full
// control of how factors are collected.
type stdioBridge struct {
	ctx         context.Context
	prompter    Prompter
	captchaFile string

	reader         *os.File
	writer         *os.File
	originalStdin  *os.File
	originalOutput io.Writer

	mu      sync.Mutex
	pending []byte
	err     error
	// inPasswordLogin is set between the upstream password request and the
	// next request; rejection holds the gateway's refusal of the password.
	inPasswordLogin bool
	rejection       string
	closed          bool
	closeOnce       sync.Once
	stop            func() bool
}

// installStdioBridge takes the bridge lock and redirects standard input and
// the standard logger. A nil prompter refuses every factor. Close restores
// both and releases the lock.
func installStdioBridge(ctx context.Context, prompter Prompter, captchaFile string) (*stdioBridge, error) {
	stdioBridgeMu.Lock()
	reader, writer, err := os.Pipe()
	if err != nil {
		stdioBridgeMu.Unlock()
		return nil, errors.New("aTrust prompt bridge is unavailable")
	}
	bridge := &stdioBridge{
		ctx:            ctx,
		prompter:       prompter,
		captchaFile:    captchaFile,
		reader:         reader,
		writer:         writer,
		originalStdin:  os.Stdin,
		originalOutput: log.Writer(),
	}
	os.Stdin = reader
	log.SetOutput(bridge)
	// Cancelling ctx closes the pipe so a pending upstream read fails at once.
	bridge.stop = context.AfterFunc(ctx, bridge.closeWriter)
	return bridge, nil
}

// Write receives standard-logger output. Each complete line is forwarded to
// the previous logger output and inspected for an upstream prompt.
func (bridge *stdioBridge) Write(data []byte) (int, error) {
	bridge.mu.Lock()
	bridge.pending = append(bridge.pending, data...)
	var lines []string
	for {
		index := bytes.IndexByte(bridge.pending, '\n')
		if index < 0 {
			break
		}
		lines = append(lines, string(bridge.pending[:index]))
		bridge.pending = bridge.pending[index+1:]
	}
	bridge.mu.Unlock()

	written, err := bridge.originalOutput.Write(data)
	for _, line := range lines {
		bridge.dispatch(line)
	}
	if err != nil {
		return written, err
	}
	return len(data), nil
}

// dispatch answers a prompt asynchronously so the logger lock is not held
// while the user responds.
func (bridge *stdioBridge) dispatch(line string) {
	bridge.observe(line)
	var answer func() ([]byte, error)
	switch {
	case strings.Contains(line, upstreamSMSPrompt):
		answer = bridge.verificationCode
	case strings.Contains(line, upstreamCaptchaPrompt):
		answer = bridge.captcha
	case strings.Contains(line, upstreamCallbackPrompt):
		// The OAuth code is always supplied up front; a callback prompt
		// means the gateway rejected it.
		answer = func() ([]byte, error) {
			return nil, errors.New("aTrust gateway rejected the OAuth authorization code")
		}
	default:
		return
	}
	bridge.mu.Lock()
	closed := bridge.closed
	bridge.mu.Unlock()
	if closed {
		return
	}
	go func() {
		value, err := answer()
		defer credential.Clear(value)
		if err != nil {
			bridge.fail(err)
			return
		}
		if bytes.ContainsAny(value, "\r\n \t") {
			bridge.fail(errors.New("aTrust authentication factor contains whitespace"))
			return
		}
		line := append(value, '\n')
		defer credential.Clear(line)
		if _, err := bridge.writer.Write(line); err != nil {
			bridge.fail(errors.New("aTrust login ended before the factor was used"))
		}
	}()
}

// observe records a gateway refusal of the password login.
func (bridge *stdioBridge) observe(line string) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	switch {
	case strings.Contains(line, upstreamPasswordLogin):
		bridge.inPasswordLogin = true
	case strings.Contains(line, upstreamRequestPrefix):
		bridge.inPasswordLogin = false
	case bridge.inPasswordLogin && bridge.rejection == "":
		match := upstreamGatewayAnswer.FindStringSubmatch(line)
		if match == nil || match[1] == "0" {
			return
		}
		detail := "gateway code " + match[1]
		if message := strings.TrimSpace(match[2]); message != "" {
			detail += ": " + message
		}
		if len(detail) > maximumRejectionDetail {
			detail = detail[:maximumRejectionDetail]
		}
		bridge.rejection = strings.ToValidUTF8(detail, "")
	}
}

// Rejection reports the gateway's refusal of the password, if it refused.
func (bridge *stdioBridge) Rejection() string {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.rejection
}

func (bridge *stdioBridge) verificationCode() ([]byte, error) {
	if bridge.prompter == nil {
		return nil, ErrSessionExpired
	}
	return bridge.prompter.VerificationCode(bridge.ctx, VerificationRequest{Channel: "sms"})
}

func (bridge *stdioBridge) captcha() ([]byte, error) {
	if bridge.prompter == nil {
		return nil, ErrSessionExpired
	}
	image, err := os.ReadFile(bridge.captchaFile)
	if err != nil {
		return nil, errors.New("aTrust captcha image is unavailable")
	}
	answer, err := bridge.prompter.Captcha(bridge.ctx, CaptchaChallenge{Image: image, MIMEType: "image/png"})
	if err != nil {
		return nil, err
	}
	return []byte(answer), nil
}

// fail records the first prompt error and closes the pipe so the upstream
// read returns instead of waiting for input that will never come.
func (bridge *stdioBridge) fail(err error) {
	bridge.mu.Lock()
	if bridge.err == nil {
		bridge.err = err
	}
	bridge.mu.Unlock()
	bridge.closeWriter()
}

func (bridge *stdioBridge) closeWriter() { _ = bridge.writer.Close() }

// Err reports why a factor could not be supplied, if one could not.
func (bridge *stdioBridge) Err() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.err
}

// Close restores standard input and the logger and releases the bridge
// lock. Call it only after the upstream Setup has returned.
func (bridge *stdioBridge) Close() {
	bridge.closeOnce.Do(func() {
		bridge.mu.Lock()
		bridge.closed = true
		bridge.mu.Unlock()
		bridge.stop()
		os.Stdin = bridge.originalStdin
		log.SetOutput(bridge.originalOutput)
		bridge.closeWriter()
		_ = bridge.reader.Close()
		stdioBridgeMu.Unlock()
	})
}
