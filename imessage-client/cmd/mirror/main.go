// Command mirror copies the Messages and Contacts databases to where the
// imessage container can read them (~/.jarvis-mirror, 0700).
//
// ~/Library/Messages is protected by macOS privacy controls, and Docker
// cannot mount it. This binary is the one thing meant to hold Full Disk
// Access for it - not the SSH server, which would hand that access to every
// remote login. It runs as the LaunchAgent com.jarvis.imessage-mirror
// (scripts/imessage-mirror install) and copies only what changed, every two
// seconds.
//
// Until the access is granted it keeps the mirror going the old way: with
// -ssh-key it runs the SSH loop (scripts/imessage-mirror.sh, the key's forced
// command) as a child, and every few minutes it exits so launchd starts it
// afresh - macOS settles a process's access when it starts, so a fresh
// process is how a new grant is seen. The first start that can read
// chat.db stops the SSH loop and copies directly from then on. The mode is
// written to <out>/mode for the status tab.
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	home, _ := os.UserHomeDir()
	out := flag.String("out", filepath.Join(home, ".jarvis-mirror"), "mirror directory")
	every := flag.Duration("every", 2*time.Second, "copy interval")
	once := flag.Bool("once", false, "copy once and exit (exit 1 if chat.db could not be read)")
	sshKey := flag.String("ssh-key", "", "without access, run the SSH mirror with this key instead")
	recheck := flag.Duration("recheck", 5*time.Minute, "without access, exit after this long so a fresh start looks again")
	flag.Parse()

	pairs := [][2]string{}
	for _, f := range []string{"chat.db", "chat.db-wal"} {
		pairs = append(pairs, [2]string{filepath.Join(home, "Library", "Messages", f), filepath.Join(*out, "messages", f)})
	}
	for _, f := range []string{"AddressBook-v22.abcddb", "AddressBook-v22.abcddb-wal"} {
		pairs = append(pairs, [2]string{filepath.Join(home, "Library", "Application Support", "AddressBook", f), filepath.Join(*out, "addressbook", f)})
	}
	for _, d := range []string{*out, filepath.Join(*out, "messages"), filepath.Join(*out, "addressbook")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			log.Fatal(err)
		}
		_ = os.Chmod(d, 0o700)
	}

	// Read the database once at start, whatever the copies look like: without
	// Full Disk Access this is where it shows.
	granted := probe(pairs[0][0])

	if !granted && *sshKey != "" && !*once {
		fallback(*out, *sshKey, *recheck)
		return
	}
	if granted && *sshKey != "" {
		// The old way may still be running from before; it is not needed now.
		stopSSHLoops()
	}
	writeMode(*out, "binary")

	last := map[string]string{}
	for {
		ok := true
		for _, p := range pairs {
			if err := copyIfChanged(p[0], p[1]); err != nil {
				ok = ok && filepath.Base(p[0]) != "chat.db"
				if last[p[0]] != err.Error() {
					log.Printf("copy %s: %v", p[0], err)
					last[p[0]] = err.Error()
				}
			} else if last[p[0]] != "" {
				log.Printf("copy %s: ok", p[0])
				last[p[0]] = ""
			}
		}
		if *once {
			if !ok {
				os.Exit(1)
			}
			return
		}
		time.Sleep(*every)
	}
}

func probe(path string) bool {
	f, err := os.Open(path)
	if err == nil {
		_, err = f.Read(make([]byte, 16))
		f.Close()
	}
	if err != nil {
		log.Printf("probe chat.db: %v", err)
		return false
	}
	log.Printf("probe chat.db: ok")
	return true
}

// fallback runs the SSH mirror until recheck has passed, then exits for
// launchd to start this again (and look at the access again).
func fallback(out, key string, recheck time.Duration) {
	writeMode(out, "ssh")
	log.Printf("전체 디스크 접근 권한이 없어 SSH 방식으로 미러링합니다. %s 뒤에 다시 확인합니다", recheck)
	stopSSHLoops() // one loop at a time
	deadline := time.Now().Add(recheck)
	for time.Now().Before(deadline) {
		cmd := exec.Command("/usr/bin/ssh", "-T", "-i", key,
			"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ServerAliveInterval=30",
			"127.0.0.1")
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("ssh: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			log.Printf("ssh 미러가 끝났습니다(%v). 다시 띄웁니다", err)
			time.Sleep(15 * time.Second)
		case <-time.After(time.Until(deadline)):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	// The loop on the far side of ssh does not end with the connection
	// (there is no terminal to hang up); it is stopped here.
	stopSSHLoops()
}

func stopSSHLoops() {
	_ = exec.Command("/usr/bin/pkill", "-f", "scripts/imessage-mirror.sh").Run()
}

func writeMode(out, mode string) {
	_ = os.WriteFile(filepath.Join(out, "mode"), []byte(mode+" "+time.Now().Format(time.RFC3339)+"\n"), 0o600)
}

// copyIfChanged copies src to dst unless both already have the same size and
// modification time. A missing source removes the copy (a WAL comes and goes).
func copyIfChanged(src, dst string) error {
	si, err := os.Stat(src)
	if os.IsNotExist(err) {
		_ = os.Remove(dst)
		return nil
	}
	if err != nil {
		return err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp, si.ModTime(), si.ModTime()); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
