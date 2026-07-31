package scan

import (
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Phase 2 (SDD): structural shell analysis. Where the regex rules match text
// line by line, this parses shell into an AST and inspects the actual command
// structure, so it catches dangerous constructs regardless of spacing, line
// continuations, or comments that defeat regexes. It runs in addition to the
// regex rules; findings dedupe by rule+file+line in scanContent.

var shellFileExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".ksh": true, ".dash": true,
}

var shellInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true, "ash": true,
}

var fetchCommands = map[string]bool{
	"curl": true, "wget": true, "fetch": true,
}

// fenceShellLangs are the markdown code-fence languages treated as shell.
var fenceShellLangs = map[string]bool{
	"sh": true, "bash": true, "shell": true, "zsh": true,
}

var shebangShellRe = regexp.MustCompile(`^#!.*\b(sh|bash|zsh|ksh|dash|ash)\b`)

// shellSnippet is a chunk of shell source plus the 1-based file line on which
// its first line sits (so AST positions map back to real file lines).
type shellSnippet struct {
	code      string
	startLine int
}

// scanShell parses shell scripts (and fenced shell blocks inside markdown) and
// appends structural findings to the report.
func scanShell(filename, content string, report *Report) {
	for _, sn := range shellSnippets(filename, content) {
		analyzeShellSnippet(filename, sn, report)
	}
}

func shellSnippets(filename, content string) []shellSnippet {
	if shellFileExts[strings.ToLower(filepath.Ext(filename))] || hasShellShebang(content) {
		return []shellSnippet{{code: content, startLine: 1}}
	}
	return markdownShellFences(content)
}

func hasShellShebang(content string) bool {
	if !strings.HasPrefix(content, "#!") {
		return false
	}
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	return shebangShellRe.MatchString(first)
}

// markdownShellFences extracts ```sh / ```bash / ```shell / ```zsh code blocks.
func markdownShellFences(content string) []shellSnippet {
	var out []shellSnippet
	lines := strings.Split(content, "\n")
	inFence := false
	var buf []string
	start := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
				if sp := strings.IndexAny(lang, " \t"); sp >= 0 {
					lang = lang[:sp]
				}
				if fenceShellLangs[lang] {
					inFence = true
					buf = nil
					start = i + 2 // first code line is the next file line (1-based)
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			out = append(out, shellSnippet{code: strings.Join(buf, "\n"), startLine: start})
			inFence = false
			continue
		}
		buf = append(buf, line)
	}
	return out
}

func analyzeShellSnippet(filename string, sn shellSnippet, report *Report) {
	if strings.TrimSpace(sn.code) == "" {
		return
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(sn.code), filename)
	if err != nil {
		return // not valid shell; the regex pass already covered the text
	}

	fileLine := func(p syntax.Pos) int { return sn.startLine - 1 + int(p.Line()) }
	add := func(rule, severity, class, msg string, pos syntax.Pos) {
		report.Findings = append(report.Findings, Finding{
			Rule:       rule,
			Severity:   severity,
			Confidence: "high",
			File:       filename,
			Line:       fileLine(pos),
			Message:    msg,
			Class:      class,
		})
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				analyzePipeline(n, add)
			}
		case *syntax.CallExpr:
			analyzeCall(n, add)
		case *syntax.Redirect:
			if n.Word != nil && strings.Contains(n.Word.Lit(), "/dev/tcp/") {
				// A reverse shell is the outbound channel itself, not a
				// guess about one: unappealable.
				add("DANGEROUS_SHELL", "critical", string(ClassExfiltration),
					"Shell AST: /dev/tcp reverse shell", n.OpPos)
			}
		}
		return true
	})
}

// analyzePipeline flags a pipeline whose final stage is a shell interpreter fed
// by a remote fetch (RCE) or a base64 decode (obfuscated execution). It is
// anchored at the final (shell) stage so nested-pipe revisits dedupe.
func analyzePipeline(bc *syntax.BinaryCmd, add func(rule, severity, class, msg string, pos syntax.Pos)) {
	var stages []*syntax.Stmt
	collectPipeStages(bc.X, &stages)
	collectPipeStages(bc.Y, &stages)
	if len(stages) < 2 {
		return
	}
	last := stages[len(stages)-1]
	if !shellInterpreters[stmtCmdName(last)] {
		return
	}
	sawFetch, sawDecode := false, false
	for _, s := range stages[:len(stages)-1] {
		name := stmtCmdName(s)
		if fetchCommands[name] {
			sawFetch = true
		}
		switch name {
		case "base64", "openssl", "xxd", "uudecode":
			sawDecode = true
		}
	}
	switch {
	case sawFetch:
		// An RCE cradle: code arriving, not data leaving. It takes the same
		// appealable class as its regex counterpart, because a network-off
		// sandbox run measures directly what this rule only guesses.
		add("DATA_EXFILTRATION", "critical", string(ClassExecution),
			"Shell AST: remote content piped to a shell (RCE pattern)", last.Pos())
	case sawDecode:
		add("OBFUSCATION", "critical", string(ClassHeuristic),
			"Shell AST: decoded content piped to a shell", last.Pos())
	}
}

func collectPipeStages(s *syntax.Stmt, out *[]*syntax.Stmt) {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && (bc.Op == syntax.Pipe || bc.Op == syntax.PipeAll) {
		collectPipeStages(bc.X, out)
		collectPipeStages(bc.Y, out)
		return
	}
	*out = append(*out, s)
}

func stmtCmdName(s *syntax.Stmt) string {
	if s == nil {
		return ""
	}
	if ce, ok := s.Cmd.(*syntax.CallExpr); ok {
		return callName(ce)
	}
	return ""
}

func callName(ce *syntax.CallExpr) string {
	if ce == nil || len(ce.Args) == 0 {
		return ""
	}
	return ce.Args[0].Lit()
}

// analyzeCall flags eval of dynamic content and `shell -c <dynamic>`.
func analyzeCall(ce *syntax.CallExpr, add func(rule, severity, class, msg string, pos syntax.Pos)) {
	name := callName(ce)
	if name == "" {
		return
	}
	if name == "eval" {
		for _, arg := range ce.Args[1:] {
			if wordHasExpansion(arg) {
				add("CODE_EXECUTION", "high", string(ClassExecution),
					"Shell AST: eval of dynamic (expanded/substituted) content", ce.Pos())
				break
			}
		}
		return
	}
	if shellInterpreters[name] {
		for i := 1; i < len(ce.Args)-1; i++ {
			if ce.Args[i].Lit() == "-c" && wordHasExpansion(ce.Args[i+1]) {
				add("CODE_EXECUTION", "high", string(ClassExecution),
					"Shell AST: shell -c executes a dynamic command string", ce.Pos())
				break
			}
		}
	}
}

// wordHasExpansion reports whether a word contains a parameter expansion,
// command/process substitution, or arithmetic expansion — i.e. dynamic content,
// as opposed to a static (possibly quoted) literal like "ls -la".
func wordHasExpansion(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	found := false
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst:
			found = true
			return false
		}
		return true
	})
	return found
}
