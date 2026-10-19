package scan

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Proc is one row of `ps`.
type Proc struct {
	PID, PPID, UID int
	CPU            float64 // %CPU (decaying average)
	RSS            int64   // bytes
	Elapsed        time.Duration
	CPUTime        time.Duration
	TTY            string
	Stat           string
	Comm           string // executable path (or process title)
	Args           string
}

// fields splits s into n whitespace-separated fields plus the untouched rest.
func fields(s string, n int) ([]string, string) {
	out := make([]string, 0, n)
	for len(out) < n {
		s = strings.TrimLeft(s, " \t")
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			if s != "" {
				out = append(out, s)
			}
			return out, ""
		}
		out = append(out, s[:i])
		s = s[i:]
	}
	return out, strings.TrimLeft(s, " \t")
}

// parseClock parses ps time formats: [[dd-]hh:]mm:ss[.cc]
func parseClock(s string) time.Duration {
	var days int
	if i := strings.IndexByte(s, '-'); i >= 0 {
		days, _ = strconv.Atoi(s[:i])
		s = s[i+1:]
	}
	parts := strings.Split(s, ":")
	var secs float64
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		secs = secs*60 + v
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs*float64(time.Second))
}

func run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// ListProcs returns every process on the system keyed by PID.
func ListProcs() (map[int]*Proc, error) {
	out, err := run("ps", "-axww", "-o", "pid=,ppid=,uid=,pcpu=,rss=,etime=,time=,tty=,stat=,comm=")
	if err != nil {
		return nil, err
	}
	procs := map[int]*Proc{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f, rest := fields(sc.Text(), 9)
		if len(f) < 9 {
			continue
		}
		p := &Proc{TTY: f[7], Stat: f[8], Comm: rest}
		p.PID, _ = strconv.Atoi(f[0])
		p.PPID, _ = strconv.Atoi(f[1])
		p.UID, _ = strconv.Atoi(f[2])
		p.CPU, _ = strconv.ParseFloat(f[3], 64)
		kb, _ := strconv.ParseInt(f[4], 10, 64)
		p.RSS = kb * 1024
		p.Elapsed = parseClock(f[5])
		p.CPUTime = parseClock(f[6])
		procs[p.PID] = p
	}

	if out, err := run("ps", "-axww", "-o", "pid=,args="); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			f, rest := fields(sc.Text(), 1)
			if len(f) == 1 {
				pid, _ := strconv.Atoi(f[0])
				if p := procs[pid]; p != nil {
					p.Args = rest
				}
			}
		}
	}
	return procs, nil
}

// Comm re-reads a single PID's executable, used to guard against PID reuse
// between scanning and killing. Empty if the process is gone.
func Comm(pid int) string {
	out, err := run("ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LaunchdJobs returns PIDs that launchd manages in the user's GUI domain.
// These have PPID 1 by design and are never "orphans".
func LaunchdJobs() map[int]bool {
	jobs := map[int]bool{}
	out, err := run("launchctl", "list")
	if err != nil {
		return jobs
	}
	for _, line := range strings.Split(string(out), "\n") {
		f, _ := fields(line, 1)
		if len(f) == 1 {
			if pid, err := strconv.Atoi(f[0]); err == nil {
				jobs[pid] = true
			}
		}
	}
	return jobs
}

// ListeningPorts maps PID -> TCP ports it is listening on (current user only).
func ListeningPorts() map[int][]int {
	ports := map[int][]int{}
	out, err := run("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-a", "-u", strconv.Itoa(os.Getuid()), "-Fpn")
	if err != nil && len(out) == 0 {
		return ports
	}
	pid := 0
	seen := map[[2]int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			i := strings.LastIndexByte(line, ':')
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(line[i+1:])
			if err != nil || seen[[2]int{pid, port}] {
				continue
			}
			seen[[2]int{pid, port}] = true
			ports[pid] = append(ports[pid], port)
		}
	}
	return ports
}
