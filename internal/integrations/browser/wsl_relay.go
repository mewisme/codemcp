package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type loopbackRelayBridge func(net.Conn, int)

type loopbackRelay struct {
	listener   net.Listener
	remotePort int
	bridge     loopbackRelayBridge

	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
}

func startWindowsLoopbackRelay(remotePort int) (*loopbackRelay, error) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return nil, fmt.Errorf("windows loopback relay requires powershell.exe: %w", err)
	}
	return startLoopbackRelay(remotePort, bridgeWindowsLoopback)
}

func startLoopbackRelay(remotePort int, bridge loopbackRelayBridge) (*loopbackRelay, error) {
	if remotePort < 1 || remotePort > 65535 {
		return nil, fmt.Errorf("invalid remote loopback port %d", remotePort)
	}
	if bridge == nil {
		return nil, fmt.Errorf("loopback relay bridge is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	relay := &loopbackRelay{
		listener: listener, remotePort: remotePort, bridge: bridge,
		connections: map[net.Conn]struct{}{},
	}
	go relay.serve()
	return relay, nil
}

func (relay *loopbackRelay) URL() string {
	if relay == nil || relay.listener == nil {
		return ""
	}
	return "http://" + relay.listener.Addr().String()
}

func (relay *loopbackRelay) serve() {
	for {
		connection, err := relay.listener.Accept()
		if err != nil {
			return
		}
		relay.mu.Lock()
		if relay.closed {
			relay.mu.Unlock()
			_ = connection.Close()
			return
		}
		relay.connections[connection] = struct{}{}
		relay.mu.Unlock()
		go relay.handle(connection)
	}
}

func (relay *loopbackRelay) handle(connection net.Conn) {
	defer func() {
		relay.mu.Lock()
		delete(relay.connections, connection)
		relay.mu.Unlock()
		_ = connection.Close()
	}()
	relay.bridge(connection, relay.remotePort)
}

func (relay *loopbackRelay) Close() error {
	if relay == nil {
		return nil
	}
	relay.mu.Lock()
	if relay.closed {
		relay.mu.Unlock()
		return nil
	}
	relay.closed = true
	listener := relay.listener
	connections := make([]net.Conn, 0, len(relay.connections))
	for connection := range relay.connections {
		connections = append(connections, connection)
	}
	relay.mu.Unlock()

	var result error
	if listener != nil {
		result = listener.Close()
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func bridgeWindowsLoopback(connection net.Conn, remotePort int) {
	script := "$c=[Net.Sockets.TcpClient]::new();" +
		"$c.Connect('127.0.0.1'," + strconv.Itoa(remotePort) + ");" +
		"$s=$c.GetStream();" +
		"$i=[Console]::OpenStandardInput();" +
		"$o=[Console]::OpenStandardOutput();" +
		"$a=$i.CopyToAsync($s);" +
		"$b=$s.CopyToAsync($o);" +
		"[Threading.Tasks.Task]::WaitAny(@($a,$b))|Out-Null;" +
		"$c.Dispose()"
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Stdin = connection
	command.Stdout = connection
	command.Stderr = io.Discard
	_ = command.Run()
}

type relayedBrowserProcess struct {
	base        BrowserProcess
	relay       *loopbackRelay
	executable  string
	profilePath string
	stopHost    func(context.Context, string, string) error
	once        sync.Once
	done        chan struct{}

	closeErr error
}

func newRelayedBrowserProcess(process BrowserProcess, relay *loopbackRelay, executable, profilePath string) BrowserProcess {
	if process == nil || relay == nil {
		return process
	}
	return &relayedBrowserProcess{
		base: process, relay: relay,
		executable: strings.TrimSpace(executable), profilePath: strings.TrimSpace(profilePath),
		stopHost: stopWindowsHostBrowser,
		done:     make(chan struct{}),
	}
}

func (process *relayedBrowserProcess) PID() int {
	if process == nil || process.base == nil {
		return 0
	}
	select {
	case <-process.base.Done():
		return 0
	default:
		return process.base.PID()
	}
}

func (process *relayedBrowserProcess) Done() <-chan struct{} {
	if process == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return process.done
}

func (process *relayedBrowserProcess) Err() error {
	if process == nil {
		return nil
	}
	return process.closeErr
}

func (process *relayedBrowserProcess) Close(ctx context.Context) error {
	if process == nil {
		return nil
	}
	process.once.Do(func() {
		if process.stopHost != nil && process.executable != "" && process.profilePath != "" {
			process.closeErr = process.stopHost(ctx, process.executable, process.profilePath)
		}
		if process.base != nil {
			process.closeErr = errors.Join(process.closeErr, process.base.Close(ctx))
		}
		if process.relay != nil {
			relayErr := process.relay.Close()
			if process.closeErr == nil {
				process.closeErr = relayErr
			}
		}
		close(process.done)
	})
	return process.closeErr
}
