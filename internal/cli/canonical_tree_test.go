package cli

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
)

var canonicalNonOperationCommandRoles = map[string]string{
	"completion": "shell-completion",
	"config":     "help",
	"tui":        "alternate-ui",
}

const frozenCanonicalCommandTree = `<root> | namespace
activity stream | operation:activity.stream
activity view | operation:activity.view
activity | namespace
agent cancel | operation:managed-agent.cancel
agent completion current | operation:completion.current
agent completion doctor | operation:completion.doctor
agent completion feed | operation:completion.feed
agent completion list | operation:completion.list
agent completion view | operation:completion.view
agent completion | namespace
agent config backend | operation:config.set:accepted-path
agent config parallel | operation:config.set:accepted-path
agent config | namespace
agent get | operation:managed-agent.get
agent list | operation:managed-agent.list
agent send | operation:managed-agent.send
agent spawn | operation:managed-agent.spawn
agent wait | operation:managed-agent.wait
agent | namespace
auth admin create | operation:auth.admin.rotate
auth admin disable | operation:auth.admin.disable
auth admin enable | operation:auth.admin.enable
auth admin | namespace
auth mcp create | operation:auth.mcp.rotate
auth mcp disable | operation:auth.mcp.disable
auth mcp enable | operation:auth.mcp.enable
auth mcp legacy bearer disable | operation:config.set:accepted-path
auth mcp legacy bearer enable | operation:config.set:accepted-path
auth mcp legacy bearer | namespace
auth mcp legacy | namespace
auth mcp | namespace
auth status | operation:auth.status
auth | namespace
completion | presentation:shell-completion
config diff | operation:config.get:accepted-path
config export | operation:config.export
config get | operation:config.get
config import | operation:config.import
config list | operation:config.list
config migrate secrets | operation:config.migrate.secrets
config migrate | operation:config.migrate
config patch | operation:config.patch
config path | operation:config.path
config reveal | operation:config.set:accepted-path
config rotate | operation:config.set:accepted-path
config set | operation:config.set
config snapshot | operation:config.snapshot.read
config unset | operation:config.set:accepted-path
config verify | operation:config.verify
config why | operation:config.get:accepted-path
config | presentation:help
doctor | operation:doctor.read
down | operation:runtime.down
execution feed | operation:execution.feed
execution list | operation:execution.list
execution stream | operation:execution.stream
execution view | operation:execution.view
execution | namespace
health | operation:health.read
http admin disable | operation:config.set:accepted-path
http admin enable | operation:config.set:accepted-path
http admin port | operation:config.set:accepted-path
http admin | namespace
http exposure interface add | operation:config.set:accepted-path
http exposure interface remove | operation:config.set:accepted-path
http exposure interface | namespace
http exposure mode | operation:config.set:accepted-path
http exposure | namespace
http mcp disable | operation:config.set:accepted-path
http mcp enable | operation:config.set:accepted-path
http mcp port | operation:config.set:accepted-path
http mcp | namespace
http security insecure allow | operation:config.set:accepted-path
http security insecure deny | operation:config.set:accepted-path
http security insecure | namespace
http security loopback auth allow | operation:config.set:accepted-path
http security loopback auth require | operation:config.set:accepted-path
http security loopback auth | namespace
http security loopback | namespace
http security | namespace
http | namespace
init | operation:config.init
install | operation:install.run
integration browser doctor | operation:integration.browser.doctor
integration browser status | operation:integration.browser.status
integration browser | namespace
integration caveman disable | operation:config.set:accepted-path
integration caveman enable | operation:config.set:accepted-path
integration caveman mode | operation:config.set:accepted-path
integration caveman | namespace
integration cf install | operation:integration.cf.install
integration cf probe | operation:integration.cf.probe
integration cf remove | operation:integration.cf.remove
integration cf status | operation:integration.cf.status
integration cf update | operation:integration.cf.update
integration cf | namespace
integration chatgpt-web doctor | operation:integration.chatgpt-web.doctor
integration chatgpt-web login | operation:integration.chatgpt-web.login
integration chatgpt-web logout | operation:integration.chatgpt-web.logout
integration chatgpt-web status | operation:integration.chatgpt-web.status
integration chatgpt-web | namespace
integration codegraph disable | operation:config.set:accepted-path
integration codegraph enable | operation:config.set:accepted-path
integration codegraph init | operation:integration.codegraph.workspace.init
integration codegraph install global | operation:integration.codegraph.install.global
integration codegraph install | operation:integration.codegraph.install
integration codegraph path | operation:config.set:accepted-path
integration codegraph probe | operation:integration.codegraph.probe
integration codegraph status | operation:integration.codegraph.status
integration codegraph sync | operation:integration.codegraph.workspace.sync
integration codegraph workspace status | operation:integration.codegraph.workspace.status
integration codegraph workspace | namespace
integration codegraph | namespace
integration fanout disable | operation:config.set:accepted-path
integration fanout enable | operation:config.set:accepted-path
integration fanout mode | operation:config.set:accepted-path
integration fanout | namespace
integration ponytail disable | operation:config.set:accepted-path
integration ponytail enable | operation:config.set:accepted-path
integration ponytail mode | operation:config.set:accepted-path
integration ponytail | namespace
integration rtk disable | operation:integration.rtk.disable
integration rtk enable | operation:integration.rtk.enable
integration rtk install global | operation:integration.rtk.install.global
integration rtk install | operation:integration.rtk.install
integration rtk path | operation:config.set:accepted-path
integration rtk probe | operation:integration.rtk.probe
integration rtk status | operation:integration.rtk.status
integration rtk | namespace
integration typesafe disable | operation:integration.typesafe.disable
integration typesafe doctor | operation:integration.typesafe.doctor
integration typesafe enable | operation:integration.typesafe.enable
integration typesafe key remove | operation:config.set:accepted-path
integration typesafe key set | operation:config.set:accepted-path
integration typesafe key | namespace
integration typesafe model | operation:config.set:accepted-path
integration typesafe probe | operation:integration.typesafe.probe
integration typesafe status | operation:integration.typesafe.status
integration typesafe timeout | operation:config.set:accepted-path
integration typesafe | namespace
integration | namespace
llm models | operation:llm.provider.models
llm ollama key clear | operation:llm.provider.credential.clear:accepted-path
llm ollama key set | operation:llm.provider.credential.set:accepted-path
llm ollama key | namespace
llm ollama mode | operation:llm.provider.configure:accepted-path
llm ollama model | operation:llm.provider.configure:accepted-path
llm ollama models | operation:llm.provider.models:accepted-path
llm ollama status | operation:llm.provider.get:accepted-path
llm ollama use | operation:llm.provider.select:accepted-path
llm ollama | namespace
llm probe | operation:llm.provider.probe
llm provider add | operation:llm.provider.add
llm provider configure | operation:llm.provider.configure
llm provider key clear | operation:llm.provider.credential.clear
llm provider key set | operation:llm.provider.credential.set
llm provider key | namespace
llm provider list | operation:llm.provider.list
llm provider remove | operation:llm.provider.remove
llm provider show | operation:llm.provider.get
llm provider | namespace
llm status | operation:llm.status
llm use | operation:llm.provider.select
llm | namespace
logs clear | operation:logs.clear
logs follow | operation:logs.follow
logs path | operation:logs.path
logs | operation:logs.read
mcp http | operation:mcp.http
mcp stdio | operation:mcp.stdio
mcp | namespace
network interfaces | operation:network.interfaces.list
network | namespace
notification approval disable | operation:config.set:accepted-path
notification approval enable | operation:config.set:accepted-path
notification approval pending disable | operation:config.set:accepted-path
notification approval pending enable | operation:config.set:accepted-path
notification approval pending | namespace
notification approval resolved disable | operation:config.set:accepted-path
notification approval resolved enable | operation:config.set:accepted-path
notification approval resolved | namespace
notification approval | namespace
notification completion desktop disable | operation:config.set:accepted-path
notification completion desktop enable | operation:config.set:accepted-path
notification completion desktop | namespace
notification completion disable | operation:config.set:accepted-path
notification completion enable | operation:config.set:accepted-path
notification completion telegram disable | operation:config.set:accepted-path
notification completion telegram enable | operation:config.set:accepted-path
notification completion telegram | namespace
notification completion | namespace
notification desktop disable | operation:config.set:accepted-path
notification desktop enable | operation:config.set:accepted-path
notification desktop | namespace
notification status | operation:notification.status
notification telegram disable | operation:config.set:accepted-path
notification telegram enable | operation:config.set:accepted-path
notification telegram | namespace
notification | namespace
permissions allow dir add | operation:config.set:accepted-path
permissions allow dir remove | operation:config.set:accepted-path
permissions allow dir | namespace
permissions allow | namespace
permissions | namespace
process clear | operation:process.clear
process list | operation:process.list
process view | operation:process.view
process | namespace
prompt create | operation:prompt.create
prompt delete | operation:prompt.delete
prompt get | operation:prompt.get
prompt list | operation:prompt.list
prompt update | operation:prompt.update
prompt | namespace
request approve | operation:request.approve
request deny | operation:request.deny
request explain mode | operation:config.set:accepted-path
request explain retry | operation:request.explain:accepted-path
request explain status | operation:request.explain.status
request explain view | operation:request.explanation.view
request explain | operation:request.explain
request grant list | operation:request.grant.list
request grant revoke | operation:request.grant.revoke
request grant | namespace
request list | operation:request.list
request stream | operation:request.stream
request view | operation:request.view
request | namespace
restart | operation:runtime.restart
serve | operation:server.foreground
shell path | operation:config.set:accepted-path
shell | namespace
skills add | operation:skill.install
skills info | operation:skill.inventory.info
skills list | operation:skill.inventory.list
skills | namespace
status | operation:status.overview
telegram logout | operation:config.set:accepted-path
telegram setup | operation:telegram.setup
telegram token remove | operation:config.set:accepted-path
telegram token set | operation:config.set:accepted-path
telegram token status | operation:config.get:accepted-path
telegram token | namespace
telegram | namespace
telemetry disable | operation:telemetry.disable
telemetry enable | operation:telemetry.enable
telemetry show | operation:telemetry.show
telemetry status | operation:telemetry.status
telemetry | namespace
tools list | operation:tools.inventory.read
tools | namespace
tui | presentation:alternate-ui
tunnel admin disable | operation:config.set:accepted-path
tunnel admin enable | operation:config.set:accepted-path
tunnel admin key remove | operation:tunnel.admin.key.remove
tunnel admin key set | operation:tunnel.admin.key.set
tunnel admin key status | operation:tunnel.admin.key.status
tunnel admin key verify | operation:tunnel.admin.key.verify
tunnel admin key | namespace
tunnel admin organization set | operation:config.set:accepted-path
tunnel admin organization | namespace
tunnel admin tenant set | operation:config.set:accepted-path
tunnel admin tenant | namespace
tunnel admin verify | operation:tunnel.admin.key.verify:accepted-path
tunnel admin workspace set | operation:config.set:accepted-path
tunnel admin workspace | namespace
tunnel admin | namespace
tunnel config | operation:tunnel.config.read
tunnel configure | operation:tunnel.configure
tunnel create | operation:tunnel.create
tunnel delete | operation:tunnel.delete
tunnel disable | operation:tunnel.disable
tunnel enable | operation:tunnel.enable
tunnel get | operation:tunnel.get
tunnel key remove | operation:tunnel.configure:accepted-path
tunnel key set | operation:tunnel.configure:accepted-path
tunnel key | namespace
tunnel list | operation:tunnel.list
tunnel run | operation:tunnel.foreground
tunnel status | operation:tunnel.status
tunnel sync | operation:tunnel.sync
tunnel update | operation:tunnel.update
tunnel use | operation:tunnel.use
tunnel | namespace
uninit | operation:config.uninit
up | operation:runtime.up
upgrade check | operation:update.check
upgrade | operation:update.apply
upstream server add | operation:upstream.server.add
upstream server auth login | operation:upstream.server.auth.login
upstream server auth logout | operation:upstream.server.auth.logout
upstream server auth status | operation:upstream.server.auth.status
upstream server auth | namespace
upstream server configure | operation:upstream.server.configure
upstream server disable | operation:upstream.server.disable
upstream server enable | operation:upstream.server.enable
upstream server list | operation:upstream.server.list
upstream server remove | operation:upstream.server.remove
upstream server show | operation:upstream.server.show
upstream server status | operation:upstream.server.status
upstream server tools | operation:upstream.server.tools
upstream server | namespace
upstream | namespace
version | operation:version.about
workspace access add | operation:workspace.access.add
workspace access list | operation:workspace.access.list
workspace access remove | operation:workspace.access.remove
workspace access | namespace
workspace container add | operation:workspace.container.add
workspace container create | operation:workspace.container.create
workspace container delete | operation:workspace.container.delete
workspace container list | operation:workspace.container.list
workspace container membership list | operation:workspace.container.membership.list
workspace container membership | namespace
workspace container remove | operation:workspace.container.remove
workspace container rename | operation:workspace.container.rename
workspace container show | operation:workspace.container.show
workspace container | namespace
workspace context | operation:project.context.read
workspace doctor | operation:workspace.show:accepted-path
workspace list | operation:workspace.list
workspace purge | operation:workspace.purge
workspace register | operation:workspace.register
workspace relocate | operation:workspace.relocate
workspace show | operation:workspace.show
workspace unregister | operation:workspace.unregister
workspace | namespace`

func TestCanonicalCommandTreeIsFrozen(t *testing.T) {
	got := strings.Join(canonicalCommandTreeInventory(newRootCommand()), "\n")
	want := strings.TrimSpace(frozenCanonicalCommandTree)
	if got != want {
		t.Fatalf("canonical command tree changed; update the frozen inventory deliberately\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestCanonicalCommandTreeHasNoSiblingNameOrAliasAmbiguity(t *testing.T) {
	root := newRootCommand()
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		if parent == nil || parent.Hidden {
			return
		}
		owners := map[string]string{}
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			names := append([]string{child.Name()}, child.Aliases...)
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name == "" {
					t.Errorf("%q declares an empty command name or alias", child.CommandPath())
					continue
				}
				if previous, exists := owners[name]; exists && previous != child.Name() {
					t.Errorf("%q has ambiguous sibling token %q shared by %q and %q", parent.CommandPath(), name, previous, child.Name())
					continue
				}
				owners[name] = child.Name()
			}
		}
		for _, child := range parent.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func TestCommandAliasesResolveToCanonicalOwner(t *testing.T) {
	root := newRootCommand()
	var walk func(*cobra.Command, []string)
	walk = func(parent *cobra.Command, parentPath []string) {
		if parent == nil || parent.Hidden {
			return
		}
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			canonicalPath := append(append([]string(nil), parentPath...), child.Name())
			canonicalOperation, hasCanonicalOperation := canonicalCommandOperation(child)
			for _, alias := range child.Aliases {
				aliasPath := append(append([]string(nil), parentPath...), alias)
				resolved, remaining, err := root.Find(aliasPath)
				if err != nil || resolved != child || len(remaining) != 0 {
					t.Errorf("alias %q resolved command=%v remaining=%v err=%v; want %q", strings.Join(aliasPath, " "), resolved, remaining, err, child.CommandPath())
					continue
				}
				if got, ok := canonicalCommandOperation(resolved); ok != hasCanonicalOperation || got != canonicalOperation {
					t.Errorf("alias %q operation=%q,%t want=%q,%t", strings.Join(aliasPath, " "), got, ok, canonicalOperation, hasCanonicalOperation)
				}
				if aliasOperation, ok := capability.ForPath(strings.Join(aliasPath, " ")); ok && (!hasCanonicalOperation || aliasOperation != canonicalOperation) {
					t.Errorf("alias %q is cataloged as separate operation %q; canonical owner=%q", strings.Join(aliasPath, " "), aliasOperation, canonicalOperation)
				}
			}
			walk(child, canonicalPath)
		}
	}
	walk(root, nil)
}

func TestRunnableCanonicalCommandsAreOperationOrPresentationOnly(t *testing.T) {
	for _, line := range canonicalCommandTreeInventory(newRootCommand()) {
		if strings.Contains(line, "runnable:unclassified") {
			t.Errorf("public runnable command lacks canonical operation or explicit presentation role: %s", line)
		}
	}
	for path, role := range canonicalNonOperationCommandRoles {
		command := commandByRelativePath(newRootCommand(), path)
		if command == nil || !command.Runnable() {
			t.Errorf("presentation-only command %q (%s) is not runnable", path, role)
			continue
		}
		if operation, ok := canonicalCommandOperation(command); ok {
			t.Errorf("presentation-only command %q (%s) unexpectedly owns operation %q", path, role, operation)
		}
	}
}

func canonicalCommandTreeInventory(root *cobra.Command) []string {
	if root == nil {
		return nil
	}
	lines := []string{}
	var walk func(*cobra.Command, []string, bool)
	walk = func(command *cobra.Command, prefix []string, hiddenAncestor bool) {
		hidden := hiddenAncestor || command.Hidden
		if hidden {
			return
		}
		pathParts := prefix
		path := "<root>"
		if command != root {
			pathParts = append(append([]string(nil), prefix...), command.Name())
			path = strings.Join(pathParts, " ")
		}
		classification := "namespace"
		if command.Runnable() {
			if operation, ok := canonicalCommandOperation(command); ok {
				classification = "operation:" + string(operation)
				if spec, found := capability.Lookup(operation); found && capability.NormalizePath(spec.CLI.CanonicalPath) != capability.NormalizePath(path) {
					classification += ":accepted-path"
				}
			} else if role := strings.TrimSpace(canonicalNonOperationCommandRoles[path]); role != "" {
				classification = "presentation:" + role
			} else {
				classification = "runnable:unclassified"
			}
		}
		lines = append(lines, fmt.Sprintf("%s | %s", path, classification))
		for _, child := range command.Commands() {
			walk(child, pathParts, hidden)
		}
	}
	walk(root, nil, false)
	sort.Strings(lines)
	return lines
}
