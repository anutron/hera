// Command hera-view-client is a standalone client for hera's plugin-view
// WebSocket. It dials ws://127.0.0.1:7744/view (or --url) directly, puts
// the local terminal in raw mode, and forwards bytes both ways — the same
// wire contract argus uses, minus argus.
//
// Use it to isolate rendering bugs: if hera-view looks correct here but
// wrong in argus, the bug is on the argus terminalpane side; if it looks
// wrong here too, the bug is hera's.
//
// Quit with Ctrl-] (0x1d) — chosen because hera-view consumes Ctrl-Q and
// the Ctrl-arrow ladder for its own focus navigation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:7744/view", "hera plugin-view WebSocket URL")
	flag.Parse()

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "stdin is not a terminal; run from a real UTF-8 terminal (iTerm, Terminal.app)")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, _, err := websocket.Dial(ctx, *url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "raw mode:", err)
		os.Exit(1)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	sendEnv := func(v any) {
		b, _ := json.Marshal(v)
		_ = c.Write(ctx, websocket.MessageText, b)
	}
	sendSize := func() {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w == 0 || h == 0 {
			w, h = 80, 24
		}
		sendEnv(map[string]any{"type": "resize", "cols": w, "rows": h})
	}
	sendSize()
	sendEnv(map[string]any{"type": "focus"})

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			sendSize()
		}
	}()

	exit := func(code int) {
		_ = c.Close(websocket.StatusNormalClosure, "client exit")
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		os.Exit(code)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		exit(0)
	}()

	// stdin → WS binary frames. Ctrl-] (0x1d) quits.
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if n == 1 && buf[0] == 0x1d {
					exit(0)
				}
				wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
				_ = c.Write(wctx, websocket.MessageBinary, append([]byte(nil), buf[:n]...))
				wcancel()
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintln(os.Stderr, "\r\nread:", err)
			return
		}
		if typ == websocket.MessageBinary {
			_, _ = os.Stdout.Write(data)
		}
	}
}
