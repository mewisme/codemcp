package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/controlplane"
)

const codeMCPModulePath = "go.mewis.me/codemcp"

type CodeMCPInvocationKind string

const (
	CodeMCPInvocationInstalled CodeMCPInvocationKind = "installed"
	CodeMCPInvocationSourceRun CodeMCPInvocationKind = "source_run"
)

type CodeMCPConfigRootSource string

const (
	CodeMCPConfigRootDefault      CodeMCPConfigRootSource = "default"
	CodeMCPConfigRootInheritedEnv CodeMCPConfigRootSource = "inherited-env"
	CodeMCPConfigRootCommandEnv   CodeMCPConfigRootSource = "command-env"
	CodeMCPConfigRootFlag         CodeMCPConfigRootSource = "flag"
	CodeMCPConfigRootEmptyFlag    CodeMCPConfigRootSource = "flag-empty"
)

type CodeMCPInvocationClassification struct {
	Recognized          bool
	Kind                CodeMCPInvocationKind
	Program             string
	Args                []string
	Command             string
	ModuleRoot          string
	SourceTarget        string
	EffectiveConfigRoot string
	ConfigRootSource    CodeMCPConfigRootSource
	UsesDefaultRoot     bool
	ExplicitEmptyFlag   bool
	OperationPath       string
	ReadOnly            bool
	Mutation            bool
	ApprovalEligible    bool
	HardDenied          bool
	Nested              bool
}

func (c CodeMCPInvocationClassification) ControlInvocation() *controlguard.Invocation {
	if !c.Recognized {
		return nil
	}
	return &controlguard.Invocation{
		Program: c.Program,
		Args:    append([]string(nil), c.Args...),
		Command: c.Command,
	}
}

func ClassifyCodeMCPInvocation(cwd, command, inheritedConfigRoot string) CodeMCPInvocationClassification {
	return classifyCodeMCPInvocationDepth(cwd, strings.TrimSpace(command), inheritedConfigRoot, 0)
}

func classifyCodeMCPInvocationDepth(cwd, command, inheritedConfigRoot string, depth int) CodeMCPInvocationClassification {
	if depth >= maxNestedShellDepth || strings.TrimSpace(command) == "" {
		return CodeMCPInvocationClassification{}
	}
	segments, err := splitShellSegments(command)
	if err != nil {
		return CodeMCPInvocationClassification{}
	}
	for _, segment := range segments {
		tokens, err := shellWords(segment)
		if err != nil || len(tokens) == 0 {
			continue
		}
		name, args, localRoot, inheritedAllowed := codeMCPInvocationCommand(tokens)
		switch {
		case isCMBinary(name):
			return classifyCodeMCPProgramInvocation(
				CodeMCPInvocationInstalled,
				cwd,
				command,
				"cm",
				args,
				"",
				"",
				localRoot,
				inheritedAllowed,
				inheritedConfigRoot,
			)
		case isGoCommand(name):
			target, programArgs, ok := codeMCPSourceRunTarget(args)
			if !ok {
				continue
			}
			moduleRoot, ok := codeMCPSourceModuleRoot(cwd, target)
			if !ok {
				continue
			}
			return classifyCodeMCPProgramInvocation(
				CodeMCPInvocationSourceRun,
				cwd,
				command,
				"cm",
				programArgs,
				moduleRoot,
				target,
				localRoot,
				inheritedAllowed,
				inheritedConfigRoot,
			)
		default:
			inner, ok := nestedShellCommand(name, args)
			if !ok {
				continue
			}
			nestedRoot := inheritedConfigRoot
			if localRoot != nil {
				nestedRoot = *localRoot
			} else if !inheritedAllowed {
				nestedRoot = ""
			}
			result := classifyCodeMCPInvocationDepth(cwd, inner, nestedRoot, depth+1)
			if !result.Recognized {
				continue
			}
			if localRoot != nil && result.ConfigRootSource == CodeMCPConfigRootInheritedEnv {
				result.ConfigRootSource = CodeMCPConfigRootCommandEnv
			}
			result.Command = command
			result.Nested = true
			return result
		}
	}
	return CodeMCPInvocationClassification{}
}

func classifyCodeMCPProgramInvocation(
	kind CodeMCPInvocationKind,
	cwd, command, program string,
	args []string,
	moduleRoot, sourceTarget string,
	localRoot *string,
	inheritedAllowed bool,
	inheritedConfigRoot string,
) CodeMCPInvocationClassification {
	flagRoot, flagChanged, emptyFlag := codeMCPConfigDirFlag(args)
	root, rootSource := "", CodeMCPConfigRootDefault
	switch {
	case flagChanged && emptyFlag:
		root, rootSource = configformat.DefaultRootPath(), CodeMCPConfigRootEmptyFlag
	case flagChanged:
		root, rootSource = flagRoot, CodeMCPConfigRootFlag
	case localRoot != nil && strings.TrimSpace(*localRoot) == "":
		root, rootSource = configformat.DefaultRootPath(), CodeMCPConfigRootCommandEnv
	case localRoot != nil:
		root, rootSource = *localRoot, CodeMCPConfigRootCommandEnv
	case inheritedAllowed && strings.TrimSpace(inheritedConfigRoot) != "":
		root, rootSource = strings.TrimSpace(inheritedConfigRoot), CodeMCPConfigRootInheritedEnv
	default:
		root = configformat.DefaultRootPath()
	}
	effectiveRoot := canonicalCodeMCPConfigRoot(cwd, root)
	defaultRoot := canonicalCodeMCPConfigRoot(cwd, configformat.DefaultRootPath())
	readOnly := controlplane.IsReadOnlyArgs(args)
	approvalEligible := controlplane.ApprovalEligibleArgs(args)
	mutation := !readOnly
	return CodeMCPInvocationClassification{
		Recognized:          true,
		Kind:                kind,
		Program:             program,
		Args:                append([]string(nil), args...),
		Command:             strings.TrimSpace(command),
		ModuleRoot:          moduleRoot,
		SourceTarget:        sourceTarget,
		EffectiveConfigRoot: effectiveRoot,
		ConfigRootSource:    rootSource,
		UsesDefaultRoot:     effectiveRoot != "" && defaultRoot != "" && sameCanonicalRoot(effectiveRoot, defaultRoot),
		ExplicitEmptyFlag:   emptyFlag,
		OperationPath:       controlplane.PathFromArgs(args),
		ReadOnly:            readOnly,
		Mutation:            mutation,
		ApprovalEligible:    approvalEligible,
		HardDenied:          mutation && !approvalEligible,
	}
}

func codeMCPInvocationCommand(tokens []string) (string, []string, *string, bool) {
	index := 0
	inheritedAllowed := true
	var localRoot *string
	for index < len(tokens) {
		token := tokens[index]
		lower := strings.ToLower(filepath.Base(token))
		switch {
		case lower == "command" || lower == "exec" || lower == "nohup":
			index++
		case lower == "env":
			index, localRoot, inheritedAllowed = parseCodeMCPEnvWrapper(tokens, index+1, localRoot, inheritedAllowed)
		case codeMCPAssignmentToken(token):
			if key, value, ok := splitCodeMCPAssignment(token); ok && strings.EqualFold(key, configformat.EnvConfigDir) {
				copyValue := value
				localRoot = &copyValue
			}
			index++
		default:
			return strings.ToLower(filepath.Base(token)), tokens[index+1:], localRoot, inheritedAllowed
		}
	}
	return "", nil, localRoot, inheritedAllowed
}

func parseCodeMCPEnvWrapper(tokens []string, index int, localRoot *string, inheritedAllowed bool) (int, *string, bool) {
	for index < len(tokens) {
		current := tokens[index]
		lowerCurrent := strings.ToLower(current)
		switch {
		case lowerCurrent == "-i" || lowerCurrent == "--ignore-environment":
			inheritedAllowed = false
			index++
		case lowerCurrent == "-u" || lowerCurrent == "--unset":
			if index+1 >= len(tokens) {
				return len(tokens), localRoot, inheritedAllowed
			}
			if strings.EqualFold(tokens[index+1], configformat.EnvConfigDir) {
				inheritedAllowed = false
				localRoot = nil
			}
			index += 2
		case strings.HasPrefix(lowerCurrent, "--unset="):
			if strings.EqualFold(strings.TrimPrefix(current, "--unset="), configformat.EnvConfigDir) {
				inheritedAllowed = false
				localRoot = nil
			}
			index++
		case codeMCPAssignmentToken(current):
			if key, value, ok := splitCodeMCPAssignment(current); ok && strings.EqualFold(key, configformat.EnvConfigDir) {
				copyValue := value
				localRoot = &copyValue
			}
			index++
		default:
			return index, localRoot, inheritedAllowed
		}
	}
	return index, localRoot, inheritedAllowed
}

func isGoCommand(name string) bool {
	name = strings.ToLower(strings.TrimSpace(filepath.Base(name)))
	return name == "go" || name == "go.exe"
}

func codeMCPSourceRunTarget(args []string) (string, []string, bool) {
	if len(args) == 0 || !strings.EqualFold(args[0], "run") {
		return "", nil, false
	}
	index := 1
	for index < len(args) {
		arg := args[index]
		if arg == "--" {
			index++
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if codeMCPGoRunFlagNeedsValue(arg) && !strings.Contains(arg, "=") {
			if index+1 >= len(args) {
				return "", nil, false
			}
			index += 2
			continue
		}
		index++
	}
	if index >= len(args) {
		return "", nil, false
	}
	target := args[index]
	if target == "" || strings.Contains(target, "...") || strings.HasSuffix(strings.ToLower(target), ".go") {
		return "", nil, false
	}
	return target, append([]string(nil), args[index+1:]...), true
}

func codeMCPGoRunFlagNeedsValue(arg string) bool {
	name := strings.ToLower(strings.TrimSpace(arg))
	if index := strings.IndexByte(name, '='); index >= 0 {
		name = name[:index]
	}
	switch name {
	case "-asmflags", "-buildmode", "-compiler", "-exec", "-gcflags", "-ldflags", "-mod", "-modfile", "-overlay", "-p", "-pkgdir", "-tags", "-toolexec":
		return true
	default:
		return false
	}
}

func codeMCPSourceModuleRoot(cwd, target string) (string, bool) {
	if strings.TrimSpace(cwd) == "" || strings.TrimSpace(target) == "" {
		return "", false
	}
	targetPath := target
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(cwd, targetPath)
	}
	info, err := os.Stat(targetPath)
	if err != nil || !info.IsDir() {
		return "", false
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return "", false
	}
	targetPath = filepath.Clean(targetPath)
	current := targetPath
	for {
		moduleFile := filepath.Join(current, "go.mod")
		if module := codeMCPModulePathFromFile(moduleFile); module != "" {
			if module != codeMCPModulePath || !sameCanonicalRoot(targetPath, current) {
				return "", false
			}
			return canonicalRoot(current), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func codeMCPModulePathFromFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			break
		}
	}
	return ""
}

func codeMCPConfigDirFlag(args []string) (string, bool, bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--config-dir" {
			if index+1 >= len(args) {
				return "", true, true
			}
			value := strings.TrimSpace(args[index+1])
			return value, true, value == ""
		}
		if strings.HasPrefix(arg, "--config-dir=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--config-dir="))
			return value, true, value == ""
		}
	}
	return "", false, false
}

func codeMCPAssignmentToken(value string) bool {
	key, _, ok := splitCodeMCPAssignment(value)
	return ok && key != "" && !strings.ContainsAny(key, "/\\")
}

func splitCodeMCPAssignment(value string) (string, string, bool) {
	index := strings.IndexByte(value, '=')
	if index <= 0 {
		return "", "", false
	}
	return value[:index], value[index+1:], true
}

func canonicalCodeMCPConfigRoot(cwd, root string) string {
	root = strings.TrimSpace(expandShellPathVariables(strings.TrimSpace(root)))
	if root == "" {
		return ""
	}
	if cwd != "" && !filepath.IsAbs(root) && !looksLikeWindowsAbsolutePath(root) {
		root = filepath.Join(cwd, root)
	}
	if canonical := canonicalRoot(root); canonical != "" {
		return canonical
	}
	return filepath.Clean(root)
}

func looksLikeWindowsAbsolutePath(path string) bool {
	if runtime.GOOS == "windows" {
		return filepath.IsAbs(path)
	}
	return len(path) >= 3 &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}
