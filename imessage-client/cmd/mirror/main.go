// Command mirror copies the Messages and Contacts databases to where the
// imessage container can read them (~/.jarvis-mirror, 0700).
//
// ~/Library/Messages is protected by macOS privacy controls, and Docker
// cannot mount it. This binary is the one thing given Full Disk Access for
// it - not the SSH server, which would hand that access to every remote
// login. It runs as the LaunchAgent com.jarvis.imessage-mirror
// (scripts/imessage-mirror install) and copies only what changed, every two
// seconds.
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	home, _ := os.UserHomeDir()
	out := flag.String("out", filepath.Join(home, ".jarvis-mirror"), "mirror directory")
	every := flag.Duration("every", 2*time.Second, "copy interval")
	once := flag.Bool("once", false, "copy once and exit (exit 1 if chat.db could not be read)")
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
	// Full Disk Access this is where it shows, and switch looks for this line.
	if f, err := os.Open(pairs[0][0]); err != nil {
		log.Printf("probe chat.db: %v", err)
	} else {
		_, err = f.Read(make([]byte, 16))
		f.Close()
		if err != nil {
			log.Printf("probe chat.db: %v", err)
		} else {
			log.Printf("probe chat.db: ok")
		}
	}

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
