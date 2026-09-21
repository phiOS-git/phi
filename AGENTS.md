# phi — the unified CLI

Go, dependency-free. One entry point for the whole system: `theme`, `state`,
`doctor`, `pkg`, `vpn`, `firewall`, `wallpaper`, `query`, `update`, `agent`,
`fan`, plus `help` / `completion zsh` / `man`.

The workspace `AGENTS.md` one level up carries the rules for every repository
and is not repeated here.

## How this repo is built

- **`cmd/phi`** is the entry point. **`internal/cli`** parses arguments,
  detects the terminal and dispatches, falling back to `phi-<verb>` on `PATH`
  for an unknown verb.
- **`internal/view`** holds every user-facing string as a pure function with
  no knowledge of terminals or process globals. `internal/view.Commands` is
  the single list `help`, the zsh completion and the man page all render
  from — adding a verb is one row.
- Each verb has its own domain package under `internal/`. **Domain logic stays
  separate from the view layer**; that separation is what keeps the choice of
  Go reversible.
- `internal/build.Version` is `"dev"` locally and set at link time by the
  PKGBUILD.
- Config it owns lives under `~/.config/phi/` and `$XDG_STATE_HOME/phi/`,
  `0600` where it matters, always outside every repository.

## The verbs

| Verb | What it does |
|---|---|
| `theme` | `render` one template, `set VARIANT [--yes\|-y]` (render every adapter target, reload what changed, record the variant — asks to turn off an active `theme.schedule` first when VARIANT differs from what is active and the call is interactive; `--yes` answers that without asking), `preview`, `list`, `check` (WCAG contrast of every checked pair), `contrast`. Generates `phi-shell`'s `Config/Tokens.qml` and `Config/Colors.json`. |
| `state` | A closed set of runtime keys under `$XDG_STATE_HOME/phi`, one flat file each. Rejects any unlisted key. |
| `doctor` | Composes eight checks: disk space, failed systemd units, dotfiles drift, SMART, declared-service status, package categories, external declarations and the `/srv` mount. Every check degrades to `unknown` rather than guessing. |
| `pkg` | `list` / `check` / `state` / `audit` / `accept` — explicitly-installed packages split by origin, with available updates, plus drift/leak/integrity auditing of declared non-official software (`external.txt`, tiers TC/T2/T3/T4). A non-empty AUR row is a policy violation and is flagged. |
| `vpn` | WireGuard: `list`, `status`, `up` / `down`, `import`, `forget`. Configs live at `~/.config/phi/wireguard/`, `0600`, outside every repo. |
| `firewall` | Inbound nftables (`inet phi`): `status`, `enable` / `disable`, `preset`, `allow`, `remove`, `log`, `blocked`. |
| `wallpaper` | `texture MODE` — a deterministic procedural PNG tile for the shell's background layer. |
| `query` | The launcher backend: ranks applications, windows, calculator, unit and currency conversion, zoxide jumps, SSH hosts, commands, web search, files and system actions. `internal/mathx` does arithmetic, units, equations, calculus and plots. |
| `update` | Snapshot, `pacman -Syu`, then regenerate every themed config. Interactive. |
| `agent` | The AI-agent subsystem: `broker`, `mcp`, `init`, `project`, `personality`, `memory`, `chat`, `search`, `session`, `code`, `ask`. |
| `fan` | `status` / `list` / `set PROFILE` — PWM fan control over the Linux hwmon ABI. Most laptops expose no PWM channel; `status` says so plainly. |

## Constraints that stay

- **Cold start is a functional requirement.** The launcher calls `phi query`
  on every keystroke. No heavyweight init on that path — order of
  milliseconds.
- **Output contract:** styled on a TTY, structured JSON when redirected. Never
  colours or spinners off-terminal. Redirected `phi query` must emit exactly
  the shape `phi-shell`'s Launcher parses.
- **Verb admission:** a verb either composes more than one tool or encodes a
  convention that exists only in this system. An alias over one command does
  not enter. If an application owns the state, the command belongs to that
  application.
- **Never verbs:** reboot, shutdown, volume, brightness, screenshot — those
  reach the shell as system actions.
- Every verb declares whether it needs a running service; without it, fail
  explicitly or queue, never hang.
- `phi theme` resolves `phios-dotfiles` via `$PHI_DOTFILES`, else
  `~/phios-dotfiles`. Fail explicitly when neither resolves; never guess.
- `sudo -n` paths (`vpn up/down`, `firewall`, `fan set`) depend on sudoers
  drop-ins shipped by `phios-dotfiles`.

## Checks

`go build ./...`, `go vet ./...` and `go test ./...` all pass. Run them before
handing work back; `GOOS=linux` is the real target.
