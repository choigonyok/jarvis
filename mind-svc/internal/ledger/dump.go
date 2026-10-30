package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Dump is a running system's judgements as files (scripts/harness dump): JSON
// lines of intents, predictions, occurrences, facts and thing classes.
type Dump struct {
	Inputs Inputs
	Facts  []model.Fact
}

func readLines[T any](path string, add func(T)) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for line := 1; sc.Scan(); line++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
		add(v)
	}
	return sc.Err()
}

// Load reads a dump.
func Load(dir string) (Dump, error) {
	var d Dump
	things := map[string]string{}
	steps := []func() error{
		func() error {
			return readLines(filepath.Join(dir, "intents.jsonl"), func(v model.Intent) { d.Inputs.Intents = append(d.Inputs.Intents, v) })
		},
		func() error {
			return readLines(filepath.Join(dir, "predictions.jsonl"), func(v model.Prediction) { d.Inputs.Predictions = append(d.Inputs.Predictions, v) })
		},
		func() error {
			return readLines(filepath.Join(dir, "occurrences.jsonl"), func(v model.Occurrence) { d.Inputs.Occurrences = append(d.Inputs.Occurrences, v) })
		},
		func() error {
			return readLines(filepath.Join(dir, "facts.jsonl"), func(v model.Fact) { d.Facts = append(d.Facts, v) })
		},
		func() error {
			return readLines(filepath.Join(dir, "things.jsonl"), func(v struct {
				Name  string `json:"name"`
				Class string `json:"class"`
			}) {
				things[v.Name] = v.Class
			})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return d, err
		}
	}
	d.Inputs.Thing = func(name string) string { return things[name] }
	return d, nil
}
