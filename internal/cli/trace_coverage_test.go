package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type commandTraceClass string

const (
	commandTraceTrivial      commandTraceClass = "trivial"
	commandTraceInstrumented commandTraceClass = "instrumented"
	commandTraceStreaming    commandTraceClass = "streaming"
)

type commandTraceContract struct {
	Class    commandTraceClass
	Expected []string
}

func commandTraceContracts() map[string]commandTraceContract {
	contracts := map[string]commandTraceContract{}
	add := func(class commandTraceClass, expected []string, paths ...string) {
		for _, path := range paths {
			contracts[path] = commandTraceContract{Class: class, Expected: append([]string(nil), expected...)}
		}
	}
	add(commandTraceTrivial, nil, "config", "logs path", "version")
	add(commandTraceTrivial, nil, "prompt list", "prompt get", "prompt create", "prompt update", "prompt delete")
	add(commandTraceStreaming, []string{"logs.snapshot.load.completed"}, "logs")
	add(commandTraceStreaming, []string{"runtime.events.connect.completed", "logs.snapshot.load.completed"}, "logs follow")
	add(commandTraceStreaming, nil, "tui")
	add(commandTraceStreaming, nil, "mcp stdio")
	add(commandTraceStreaming, nil, "mcp http")
	add(commandTraceInstrumented, []string{"server.config.load.completed", "runtime.session.completed"}, "<root>", "serve", "_service run")
	add(commandTraceInstrumented, []string{"service.backend.start.completed", "service.runtime.ready.wait.completed"}, "up")
	add(commandTraceInstrumented, []string{"service.backend.stop.completed"}, "down")
	add(commandTraceInstrumented, []string{"service.backend.stop.completed", "service.backend.start.completed"}, "restart")
	add(commandTraceInstrumented, []string{"config.initialize.completed", "config.persist.completed"}, "init")
	add(commandTraceInstrumented, []string{"config.uninitialize.completed", "config.root.remove.completed"}, "uninit")
	add(commandTraceInstrumented, []string{"install.apply.completed"}, "install")
	add(commandTraceInstrumented, []string{"auth.token.rotate.completed"}, "auth admin create", "auth mcp create")
	add(commandTraceInstrumented, []string{"auth.state.set.completed"}, "auth admin disable", "auth admin enable", "auth mcp disable", "auth mcp enable")
	add(commandTraceInstrumented, []string{"auth.status.completed"}, "auth status")
	add(commandTraceInstrumented, []string{"completion.script.generate.completed", "completion.generate.completed"}, "completion")
	add(commandTraceInstrumented, []string{"config.source.inspect.completed"}, "config path")
	add(commandTraceInstrumented, []string{"setting.read.completed"}, "config get", "config list", "config diff")
	add(commandTraceInstrumented, []string{"config.why.lookup.completed", "config.why.render.completed"}, "config why")
	add(commandTraceInstrumented, []string{"setting.set.completed"}, "config set")
	add(commandTraceInstrumented, []string{"setting.set.completed"},
		"server enable", "server disable", "server port", "server expose mode", "server interface add", "server interface remove",
		"server insecure http allow", "server insecure http deny", "server loopback auth allow", "server loopback auth require",
		"admin enable", "admin disable", "admin port",
		"auth mcp legacy bearer enable", "auth mcp legacy bearer disable",
		"permissions allow dir add", "permissions allow dir remove",
		"shell path",
		"notification approval enable", "notification approval disable",
		"notification approval pending enable", "notification approval pending disable",
		"notification approval resolved enable", "notification approval resolved disable",
		"notification desktop enable", "notification desktop disable",
		"notification telegram enable", "notification telegram disable",
		"notification completion enable", "notification completion disable",
		"notification completion desktop enable", "notification completion desktop disable",
		"notification completion telegram enable", "notification completion telegram disable",
		"integration ponytail enable", "integration ponytail disable", "integration ponytail mode",
		"integration caveman enable", "integration caveman disable", "integration caveman mode",
		"integration rtk enable", "integration rtk disable", "integration rtk path",
		"integration codegraph enable", "integration codegraph disable", "integration codegraph path",
		"integration typesafe enable", "integration typesafe disable", "integration typesafe model", "integration typesafe timeout", "integration typesafe key set",
		"tunnel admin organization set", "tunnel admin workspace set", "tunnel admin tenant set",
		"tunnel admin enable", "tunnel admin disable", "tunnel key set",
	)
	add(commandTraceInstrumented, []string{"setting.unset.completed"}, "config unset", "tunnel key remove", "tunnel admin key remove", "integration typesafe key remove")
	add(commandTraceTrivial, nil, "integration typesafe status", "integration typesafe doctor", "integration typesafe probe")
	add(commandTraceInstrumented, []string{"setting.rotate.completed"}, "config rotate")
	add(commandTraceInstrumented, []string{"setting.reveal.completed"}, "config reveal")
	add(commandTraceInstrumented, []string{"config.secrets.migrate.completed"}, "config migrate")
	add(commandTraceInstrumented, []string{"config.secrets.envelope.migrate.completed"}, "config migrate secrets")
	add(commandTraceInstrumented, []string{"config.export.completed"}, "config export")
	add(commandTraceInstrumented, []string{"config.import.completed"}, "config import")
	add(commandTraceInstrumented, []string{"config.verify.completed"}, "config verify")
	add(commandTraceInstrumented, []string{"logs.clear.completed"}, "logs clear")
	add(commandTraceInstrumented, []string{"upstream.store.load.completed"}, "upstream server list", "upstream server show")
	add(commandTraceInstrumented, []string{"upstream.server.save.completed"}, "upstream server add", "upstream server configure", "upstream server disable", "upstream server enable")
	add(commandTraceInstrumented, []string{"upstream.server.remove.completed"}, "upstream server remove")
	add(commandTraceInstrumented, []string{"upstream.status.list.completed"}, "upstream server status")
	add(commandTraceInstrumented, []string{"upstream.tools.discover.completed", "upstream.tools.list.completed"}, "upstream server tools")
	add(commandTraceInstrumented, []string{"oauth.login.completed"}, "upstream server auth login")
	add(commandTraceInstrumented, []string{"oauth.store.delete.completed"}, "upstream server auth logout")
	add(commandTraceInstrumented, []string{"oauth.store.status.completed"}, "upstream server auth status")
	add(commandTraceInstrumented, []string{"request.list.completed"}, "request list")
	add(commandTraceInstrumented, []string{"request.view.completed"}, "request view")
	add(commandTraceInstrumented, []string{"request.resolve.completed"}, "request approve", "request deny")
	add(commandTraceInstrumented, []string{"request.grants.completed"}, "request grant list")
	add(commandTraceInstrumented, []string{"request.revoke-grant.completed"}, "request grant revoke")
	add(commandTraceInstrumented, []string{"request.create-dummy.completed"}, "request create dummy")
	add(commandTraceInstrumented, []string{"completion.current.completed"}, "agent completion current")
	add(commandTraceInstrumented, []string{"completion.doctor.completed"}, "agent completion doctor")
	add(commandTraceInstrumented, []string{"completion.list.completed"}, "agent completion list")
	add(commandTraceInstrumented, []string{"completion.view.completed"}, "agent completion view")
	add(commandTraceInstrumented, []string{"status.snapshot.completed"}, "status")
	add(commandTraceInstrumented, []string{"setting.set.completed"}, "tunnel admin key set")
	add(commandTraceInstrumented, []string{"tunnel.admin-key.status.completed"}, "tunnel admin key status")
	add(commandTraceInstrumented, []string{"setting.verify.completed"}, "tunnel admin key verify", "tunnel admin verify")
	add(commandTraceInstrumented, []string{"tunnel.admin.list.completed"}, "tunnel list")
	add(commandTraceInstrumented, []string{"tunnel.admin.get.completed"}, "tunnel get")
	add(commandTraceInstrumented, []string{"tunnel.managed.use.completed"}, "tunnel use")
	add(commandTraceInstrumented, []string{"tunnel.admin.create.completed"}, "tunnel create")
	add(commandTraceInstrumented, []string{"tunnel.admin.update.completed"}, "tunnel update")
	add(commandTraceInstrumented, []string{"tunnel.admin.delete.completed"}, "tunnel delete")
	add(commandTraceInstrumented, []string{"tunnel.runtime.configure.completed"}, "tunnel configure", "tunnel disable", "tunnel enable")
	add(commandTraceInstrumented, []string{"status.tunnel.fetch.completed"}, "tunnel status")
	add(commandTraceInstrumented, []string{"tunnel.metadata.fetch.completed"}, "tunnel sync")
	add(commandTraceInstrumented, []string{"tunnel.metadata.fetch.completed"}, "tunnel run")
	add(commandTraceInstrumented, []string{"update.apply.completed"}, "upgrade")
	add(commandTraceInstrumented, []string{"update.release.resolve.completed"}, "upgrade check")
	add(commandTraceInstrumented, []string{"workspace.register.completed", "workspace.registry.persist.completed"}, "workspace register")
	add(commandTraceInstrumented, []string{"workspace.relocate.completed", "workspace.registry.persist.completed"}, "workspace relocate")
	add(commandTraceInstrumented, []string{"workspace.unregister.completed", "workspace.registry.persist.completed"}, "workspace unregister")
	add(commandTraceInstrumented, []string{"workspace.delete-state.completed"}, "workspace purge")
	add(commandTraceInstrumented, []string{"workspace.registry.load.completed"}, "workspace list", "workspace show", "workspace doctor", "workspace access list", "workspace container list", "workspace container show")
	add(commandTraceInstrumented, []string{"workspace.allow-dir.add.completed"}, "workspace access add")
	add(commandTraceInstrumented, []string{"workspace.allow-dir.remove.completed"}, "workspace access remove")
	add(commandTraceInstrumented, []string{"workspace.container.create.completed"}, "workspace container create")
	add(commandTraceInstrumented, []string{"workspace.container.rename.completed"}, "workspace container rename")
	add(commandTraceInstrumented, []string{"workspace.container.delete.completed"}, "workspace container delete")
	add(commandTraceInstrumented, []string{"workspace.container.membership.completed"}, "workspace container add", "workspace container remove")
	return contracts
}

func TestExecutableCommandsHaveTraceClassification(t *testing.T) {
	root := newRootCommand()
	actual := executableCommandPaths(root)
	contracts := commandTraceContracts()
	for _, path := range actual {
		contract, ok := contracts[path]
		if !ok {
			t.Errorf("executable command %q has no trace classification", path)
			continue
		}
		switch contract.Class {
		case commandTraceTrivial, commandTraceInstrumented, commandTraceStreaming:
		default:
			t.Errorf("executable command %q has invalid trace class %q", path, contract.Class)
		}
		if contract.Class == commandTraceInstrumented && len(contract.Expected) == 0 {
			t.Errorf("instrumented command %q has no expected trace sequence", path)
		}
	}
	actualSet := map[string]bool{}
	for _, path := range actual {
		actualSet[path] = true
	}
	for path := range contracts {
		if !actualSet[path] {
			t.Errorf("stale trace classification for non-executable command %q", path)
		}
	}
}

func TestInstrumentedCommandsDefineStableTraceSequence(t *testing.T) {
	for path, contract := range commandTraceContracts() {
		if contract.Class != commandTraceInstrumented {
			continue
		}
		seen := map[string]bool{}
		for _, name := range contract.Expected {
			if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "*?[]") {
				t.Errorf("instrumented command %q has unstable trace event %q", path, name)
			}
			if seen[name] {
				t.Errorf("instrumented command %q repeats trace event %q", path, name)
			}
			seen[name] = true
		}
	}
}

func TestTraceContractsReferenceDeclaredEvents(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve trace coverage test source path")
	}
	internalRoot := filepath.Dir(filepath.Dir(file))
	var source strings.Builder
	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source.Write(data)
		source.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	text := source.String()
	for path, contract := range commandTraceContracts() {
		for _, event := range contract.Expected {
			base := traceContractBaseName(event)
			needle := `"` + base + `"`
			if strings.HasPrefix(base, "service.backend.") {
				needle = `"service.backend."`
			}
			if !strings.Contains(text, needle) {
				t.Errorf("trace contract for %q references undeclared event %q", path, event)
			}
		}
	}
}

func traceContractBaseName(name string) string {
	for _, suffix := range []string{".completed", ".failed", ".started"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func executableCommandPaths(root *cobra.Command) []string {
	paths := []string{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Run != nil || cmd.RunE != nil {
			path := strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), root.Name()))
			if cmd == root {
				path = "<root>"
			}
			paths = append(paths, path)
		}
		for _, child := range cmd.Commands() {
			if child.Name() == "help" {
				continue
			}
			walk(child)
		}
	}
	walk(root)
	sort.Strings(paths)
	return paths
}
