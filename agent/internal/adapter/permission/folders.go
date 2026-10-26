package permission

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Folders are the only places the file tools may reach - the guest's agent's
// photos and memory. A call inside them goes through without a card; any
// other is refused, never asked: a file tool let loose could read this
// process's environment, and a card would only let a page talk someone into
// approving that.
type Folders []string

// fileTools are the built-in tools that touch the disk, and where each names
// its path.
var fileTools = map[string][]string{
	"Read": {"file_path"}, "Write": {"file_path"}, "Edit": {"file_path"}, "MultiEdit": {"file_path"},
	"NotebookEdit": {"notebook_path"}, "Glob": {"path"}, "Grep": {"path"}, "LS": {"path"}, "Bash": nil,
}

// Judge says whether tool touches the disk, and if so whether the path it
// names is inside one of the folders. A file tool with no path of its own (a
// shell, a search from wherever it stands) is never inside.
func (f Folders) Judge(tool string, input json.RawMessage) (file, inside bool) {
	keys, file := fileTools[tool]
	if !file {
		return false, false
	}
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	for _, k := range keys {
		p, _ := in[k].(string)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		for _, dir := range f {
			if dir = filepath.Clean(dir); dir != "." && dir != "/" && strings.HasPrefix(p, dir+string(filepath.Separator)) {
				return true, true
			}
		}
	}
	return true, false
}
