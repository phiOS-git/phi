package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// `phi agent ask` is a quick terminal question: one pi print-mode run
// (`pi -p --no-session`, §6), no session kept — it never appears in the
// panel list or reaches memory. Fail-closed: BuildLaunch itself refuses to
// start when the containment launcher or the named project is missing.

// AskConfig is the small set of knobs `phi agent ask` needs.
type AskConfig struct {
	Profile Profile // general or academic
	Project string  // "" = none; must already exist when set
	Timeout time.Duration
}

// Ask sends one question through pi in print mode and writes its reply to w.
func Ask(ctx context.Context, cfg AskConfig, question string, w io.Writer) error {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	argv, err := BuildLaunch(LaunchSpec{
		Profile: cfg.Profile,
		Project: cfg.Project,
		Mode:    ModePrint,
		Prompt:  question,
	})
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("phi agent ask: pi exited with an error: %w", err)
	}
	return nil
}
