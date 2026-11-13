// Package shell 是执行外部命令的唯一入口。
//
// 所有系统变更都通过它执行，这样 --dry-run 只需把 Runner 设成不执行，
// 就能在真机上完整走一遍流程而不碰系统——比在代码里模拟更可信。
package shell

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Runner 执行外部命令。DryRun 为真时只打印不执行。
type Runner struct {
	DryRun bool
	Out    io.Writer
	// Indent 是日志行的前缀空格数，用于对齐到步骤内容下
	Indent int
}

// New 创建一个实际执行的 Runner。
func New(out io.Writer) *Runner {
	return &Runner{Out: out, Indent: 6}
}

// Dry 创建一个只打印不执行的 Runner。
func Dry(out io.Writer) *Runner {
	return &Runner{DryRun: true, Out: out, Indent: 6}
}

// Run 执行命令并等待结束，返回错误时带上命令的可读形式。
func (r *Runner) Run(name string, args ...string) error {
	if r.DryRun {
		r.printf("  $ %s", strings.Join(append([]string{name}, args...), " "))
		return nil
	}
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("%s 失败: %w\n%s", name, err, r.indent(trimmed))
		}
		return fmt.Errorf("%s 失败: %w", name, err)
	}
	return nil
}

// Output 执行命令并返回标准输出，用于需要读取结果的场景。
func (r *Runner) Output(name string, args ...string) (string, error) {
	if r.DryRun {
		r.printf("  $ %s", strings.Join(append([]string{name}, args...), " "))
		return "", nil
	}
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return string(out), fmt.Errorf("%s 失败: %w", name, err)
	}
	return string(out), nil
}

// Has 判断命令是否存在于 PATH。
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (r *Runner) printf(format string, args ...any) {
	if r.Out == nil {
		return
	}
	fmt.Fprintf(r.Out, "%s"+format+"\n", append([]any{strings.Repeat(" ", r.Indent)}, args...)...)
}

func (r *Runner) indent(text string) string {
	pad := strings.Repeat(" ", r.Indent+2)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}
