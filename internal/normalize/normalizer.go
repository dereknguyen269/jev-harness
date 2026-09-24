package normalize

import (
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/policy"
)

// terminalNames maps every known agent-specific shell tool to canonical terminal.
var terminalNames = map[string]bool{
	"terminal": true, "bash": true, "execute": true, "shell": true,
	"execute_bash": true, "executeBash": true, "Bash": true,
}

// writeNames maps filesystem mutation tools to canonical write_file.
var writeNames = map[string]bool{
	"write_file": true, "write": true, "Write": true,
	"fs_write": true, "fs_append": true, "str_replace": true,
	"Edit": true, "apply_patch": true, "edit": true,
}

// readNames maps file read tools to canonical read_file.
var readNames = map[string]bool{
	"read_file": true, "read": true, "Read": true, "cat": true,
}

// CanonicalName maps an agent-specific tool name to its canonical form.
// Policy engines must only switch on the return value.
func CanonicalName(name string) domain.CanonicalTool {
	switch {
	case terminalNames[name]:
		return domain.ToolTerminal
	case writeNames[name]:
		return domain.ToolWriteFile
	case readNames[name]:
		return domain.ToolReadFile
	case name == "delete_file" || name == "deleteFile" || name == "Delete":
		return domain.ToolDeleteFile
	case name == "smart_relocate" || name == "move" || name == "rename":
		// Relocate removes the source → mutation class, destination path used.
		return domain.ToolWriteFile
	case name == "patch":
		return domain.ToolPatch
	case name == "browser_navigate" || name == "browser" || name == "browser_extract" ||
		name == "network" || name == "fetch" || name == "WebFetch":
		return domain.ToolNetwork
	default:
		return domain.ToolUnknown
	}
}

// Normalize converts a V2 ToolRequest into a NormalizedRequest by reusing the
// battle-tested v1 policy.Normalize extractor, then overlaying the canonical
// tool name. Adapters stay thin: they only supply name+args.
func Normalize(req domain.ToolRequest) domain.NormalizedRequest {
	ta := policy.Normalize(req.Tool.Name, req.Tool.Args)
	canonical := CanonicalName(req.Tool.Name)
	// Delete-family tools are destructive writes regardless of extractor output.
	if canonical == domain.ToolDeleteFile {
		canonical = domain.ToolWriteFile
		ta.Destructive = true
	}
	if canonical == domain.ToolTerminal {
		ta.Tool = "terminal"
	}
	if canonical == domain.ToolWriteFile && ta.Tool != "write_file" {
		// Keep v1 rule compat: file rules match on write_file.
		if ta.Tool == "unknown" || ta.Tool == req.Tool.Name {
			ta.Tool = "write_file"
		}
	}
	return domain.NormalizedRequest{
		Request:     req,
		Canonical:   canonical,
		Command:     ta.Command,
		Path:        ta.Path,
		URL:         ta.URL,
		Resource:    ta.Resource,
		Destructive: ta.Destructive,
		Network:     ta.Network,
		Sensitive:   ta.Sensitive,
	}
}
