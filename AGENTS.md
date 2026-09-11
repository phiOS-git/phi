# phi — the unified CLI

Go, dependency-free (`go.mod` names the module `phi`, never imported
elsewhere). One entry point for the whole system: `theme`, `state`,
`doctor`, `pkg`, `vpn`, `firewall`, `wallpaper`, `query`, `update`,
`agent`, plus `help` / `completion zsh` / `man`.

Part of the phiOS workspace. The workspace `AGENTS.md` (one level up, or in
`docs/archive/` of a standalone clone) carries the rules that apply to
every repository — **branch locally, only `main`/`dev` on the remote; only
official Arch packages; the user owns package releases, an agent only tags;
no secrets in a public repo.** They are not repeated here.

When your work matches an entry in the workspace's `docs/TODO.md`, claim it
with `[taken]` and report the result in `docs/VERIFICATION.md` — see *The
TODO / VERIFICATION loop* in the workspace `AGENTS.md`.

## How this repo is built

- **`cmd/phi`** is the entry point. **`internal/cli`** parses arguments,
  detects the terminal, and dispatches — falling back to `phi-<verb>` on
  `PATH` for an unknown verb (ADR 017).
- **`internal/view`** holds every user-facing string as a pure function
  with no knowledge of terminals or process globals. `internal/view.Commands`
  is the single list that `help`, the zsh completion and the man page all
  render from — adding a verb is one row there.
- Each verb has its domain package under `internal/` (`theme`, `state`,
  `doctor`, `firewall`, `vpn`, `wallpaper`, `query`, `mathx`, `agent`,
  `tokens`, `pkg`, `update`). **Keep domain logic separate from the view
  layer from the first line** — it is the only thing keeping the Go choice
  reversible.
- `internal/build.Version` is `"dev"` locally and set at link time by the
  PKGBUILD (`-ldflags -X phi/internal/build.Version=`).
- Config it owns lives under `~/.config/phi/` and `$XDG_STATE_HOME/phi/`,
  `0600` where it matters, always outside every repository.

## Constraints that stay

- **Cold start is a functional requirement.** The launcher calls
  `phi query` on every keystroke. No heavyweight init, no config parsing on
  that path. Order of milliseconds.
- **Output contract:** styled on a TTY, structured (JSON) when redirected.
  Never colours or spinners off-terminal. `phi query` redirected must emit
  exactly the shape `phi-shell`'s Launcher parses.
- **Verb admission:** a verb either composes more than one tool or encodes
  a convention that exists only in this system. An alias over one command
  does not enter. If an application owns the state, the command belongs to
  that application.
- **Never verbs:** reboot, shutdown, volume, brightness, screenshot — those
  reach the shell as system actions, not `phi` verbs.
- Every verb declares whether it needs a running service; without it, fail
  explicitly or queue — never hang.
- `phi theme` finds `phios-dotfiles` via `$PHI_DOTFILES`, else
  `~/phios-dotfiles`. Fail explicitly when neither resolves; never guess.
- `sudo -n` paths (`vpn up/down`, `firewall`) depend on sudoers drop-ins
  shipped by `phios-dotfiles`; assume nothing else.

## Releasing

Bump the version by tagging: `git tag vX.Y.Z && git push origin vX.Y.Z` on
`main`. That is the whole of an agent's role in a release. The user builds
the signed package from that tag in `phi-packages` and publishes it.
