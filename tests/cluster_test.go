package tests

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Helpers that start a private broker and worker nodes for the benchmarks and
// the trace test, so they don't need you to start anything by hand.

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// buildBinaries compiles the broker and worker once per `go test` run.
// TestMain has already moved the working directory to the repo root.
func buildBinaries() (string, error) {
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "gol-bench")
		if buildErr != nil {
			return
		}
		for _, pkg := range []string{"server", "worker"} {
			out, err := exec.Command("go", "build", "-o", filepath.Join(binDir, pkg), "./"+pkg).CombinedOutput()
			if err != nil {
				buildErr = fmt.Errorf("building %s: %v\n%s", pkg, err, out)
				return
			}
		}
	})
	return binDir, buildErr
}

func freeAddr(tb testing.TB) string {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// AWS mode: pass these to `go test` to run the broker and workers on EC2
// instead of locally. Each host is "PUBLIC_IP/PRIVATE_IP". The server and
// worker binaries must already be in ec2-user's home directory.
var (
	awsBroker  = flag.String("aws-broker", "", "broker EC2 host as PUBLIC_IP/PRIVATE_IP")
	awsWorkers = flag.String("aws-workers", "", "comma-separated worker EC2 hosts as PUBLIC_IP/PRIVATE_IP")
	awsKey     = flag.String("aws-key", filepath.Join(os.Getenv("HOME"), ".ssh", "labsuser.pem"), "SSH key for the EC2 instances")
)

type ec2Host struct{ public, private string }

func parseHost(s string) (ec2Host, error) {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 2 {
		return ec2Host{}, fmt.Errorf("%q should be PUBLIC_IP/PRIVATE_IP", s)
	}
	return ec2Host{public: parts[0], private: parts[1]}, nil
}

func awsMode() bool { return *awsBroker != "" }

// awsWorkerHosts returns the worker hosts given with -aws-workers.
func awsWorkerHosts(tb testing.TB) []ec2Host {
	var hosts []ec2Host
	for _, s := range strings.Split(*awsWorkers, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		h, err := parseHost(s)
		if err != nil {
			tb.Fatal(err)
		}
		hosts = append(hosts, h)
	}
	return hosts
}

func ssh(host ec2Host, command string) error {
	out, err := exec.Command("ssh", "-i", *awsKey,
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10",
		"ec2-user@"+host.public, command).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh %s %q: %v\n%s", host.public, command, err, out)
	}
	return nil
}

// startAWSCluster restarts the broker and the first `nodes` workers on EC2 over SSH,
// so the broker only has exactly those workers subscribed.
func startAWSCluster(tb testing.TB, nodes int) (stop func()) {
	tb.Helper()

	broker, err := parseHost(*awsBroker)
	if err != nil {
		tb.Fatal(err)
	}
	workers := awsWorkerHosts(tb)
	if nodes > len(workers) {
		tb.Fatalf("asked for %d workers but only %d given in -aws-workers", nodes, len(workers))
	}

	killAll := func() {
		for _, w := range workers {
			ssh(w, "pkill -x worker; true")
		}
		ssh(broker, "pkill -x server; true")
	}

	// start from a clean slate, since the broker never forgets a worker
	killAll()
	time.Sleep(time.Second)

	if err := ssh(broker, "nohup ./server > server.log 2>&1 < /dev/null &"); err != nil {
		tb.Fatal(err)
	}
	brokerPublic := broker.public + ":8030"
	ready := false
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", brokerPublic, 2*time.Second)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		killAll()
		tb.Fatalf("could not reach the broker at %s", brokerPublic)
	}

	for _, w := range workers[:nodes] {
		start := fmt.Sprintf("nohup ./worker -ip %s:8040 -broker %s:8030 > worker.log 2>&1 < /dev/null &", w.private, broker.private)
		waitSubscribed := "for i in $(seq 50); do grep -q 'Listening for jobs' worker.log && exit 0; sleep 0.2; done; cat worker.log; exit 1"
		if err := ssh(w, start); err != nil {
			killAll()
			tb.Fatal(err)
		}
		if err := ssh(w, waitSubscribed); err != nil {
			killAll()
			tb.Fatalf("worker %s never subscribed: %v", w.public, err)
		}
	}

	brokerFlag := flag.Lookup("broker")
	oldAddr := brokerFlag.Value.String()
	brokerFlag.Value.Set(brokerPublic)

	return func() {
		brokerFlag.Value.Set(oldAddr)
		killAll()
	}
}

// startCluster starts a broker and `nodes` worker nodes, points the
// controller's -broker flag at that broker, and returns a function that stops
// them all and restores the flag. With -aws-broker set it uses EC2 instead of
// local processes.
func startCluster(tb testing.TB, nodes int) (stop func()) {
	tb.Helper()

	if awsMode() {
		return startAWSCluster(tb, nodes)
	}

	dir, err := buildBinaries()
	if err != nil {
		tb.Fatal(err)
	}

	brokerAddr := freeAddr(tb)
	var procs []*exec.Cmd
	stop = func() {
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].Process.Kill()
			procs[i].Wait()
		}
	}

	broker := exec.Command(filepath.Join(dir, "server"), "-port", brokerAddr)
	if err := broker.Start(); err != nil {
		tb.Fatal(err)
	}
	procs = append(procs, broker)

	// wait until the broker is accepting connections
	ready := false
	for i := 0; i < 100; i++ {
		conn, err := net.Dial("tcp", brokerAddr)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		stop()
		tb.Fatalf("broker never started on %s", brokerAddr)
	}

	for i := 0; i < nodes; i++ {
		// port 0 lets the OS pick a free port; the worker subscribes with the real one
		worker := exec.Command(filepath.Join(dir, "worker"), "-ip", "127.0.0.1:0", "-broker", brokerAddr)
		stdout, err := worker.StdoutPipe()
		if err != nil {
			stop()
			tb.Fatal(err)
		}
		if err := worker.Start(); err != nil {
			stop()
			tb.Fatal(err)
		}
		procs = append(procs, worker)

		// the worker prints this line once Subscribe has returned
		subscribed := make(chan bool, 1)
		go func(r io.Reader) {
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				if strings.Contains(scanner.Text(), "Listening for jobs") {
					subscribed <- true
					break
				}
			}
			io.Copy(io.Discard, r) // keep draining so the worker never blocks on stdout
		}(stdout)

		select {
		case <-subscribed:
		case <-time.After(10 * time.Second):
			stop()
			tb.Fatalf("worker %d never subscribed to the broker", i)
		}
	}

	brokerFlag := flag.Lookup("broker")
	oldAddr := brokerFlag.Value.String()
	brokerFlag.Value.Set(brokerAddr)

	killAll := stop
	return func() {
		brokerFlag.Value.Set(oldAddr)
		killAll()
	}
}
