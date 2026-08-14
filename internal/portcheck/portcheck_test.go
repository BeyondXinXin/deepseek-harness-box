package portcheck

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPreferredPorts(t *testing.T) {
	ports := PreferredPorts()
	if len(ports) != PortEnd-PortStart+1 {
		t.Fatalf("expected %d ports, got %d", PortEnd-PortStart+1, len(ports))
	}
	if ports[0] != DefaultPort {
		t.Fatalf("first port should be %d, got %d", DefaultPort, ports[0])
	}
	if ports[len(ports)-1] != PortStart {
		t.Fatalf("last port should be %d, got %d", PortStart, ports[len(ports)-1])
	}
	seen := make(map[int]bool)
	for _, p := range ports {
		if p < PortStart || p > PortEnd {
			t.Fatalf("port %d out of range", p)
		}
		if seen[p] {
			t.Fatalf("duplicate port %d", p)
		}
		seen[p] = true
	}
}

func TestParseNetstatPID(t *testing.T) {
	output := `  TCP    0.0.0.0:135        0.0.0.0:0              LISTENING       1234
  TCP    127.0.0.1:3081     0.0.0.0:0              LISTENING       5555
  TCP    127.0.0.1:3081     127.0.0.1:5555         ESTABLISHED     9999
`
	pid, err := parseNetstatPID(output, 3081)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 5555 {
		t.Fatalf("expected pid 5555, got %d", pid)
	}
	if _, err := parseNetstatPID(output, 3999); err == nil {
		t.Fatal("expected error for free port")
	}
}

func TestInspectOccupied(t *testing.T) {
	const testPort = 3998
	listener, err := net.Listen("tcp4", net.JoinHostPort(Host, strconv.Itoa(testPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	info, err := Inspect(Host, testPort)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Occupied {
		t.Fatalf("expected port %d occupied", testPort)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), info.PID)
	}
}

func TestIsDSHOrHarnessBox(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}

	// 造一个“像 DSH”的 node 进程：node <dir>/bin.js（命令行包含 bin.js）。
	dir := t.TempDir()
	script := filepath.Join(dir, "bin.js")
	if err := os.WriteFile(script, []byte("setInterval(() => {}, 1000);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	if !IsDSHOrHarnessBox(cmd.Process.Pid) {
		t.Fatalf("node running bin.js should be detected as DSH")
	}
	if IsDSHOrHarnessBox(os.Getpid()) {
		t.Fatalf("test process should not be detected as DSH")
	}
}
