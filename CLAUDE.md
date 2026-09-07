# phi — unified CLI

Go. ADR 016. ADR 017: monolith with a `PATH` fallback to `phi-<name>`.

- **Separate domain logic from the view layer from the first line.** Domain logic ports mechanically to another language; a TUI view layer does not. This is the only thing keeping ADR 016 reversible.
- **Cold start is a functional requirement**: the launcher invokes `phi query` on every keystroke. Order of milliseconds. No heavyweight init, no config parsing on the hot path.
- Output contract: styled on a TTY, structured when redirected. **Never** colours or spinners off-terminal.
- Verb admission test (ADR 021), applied to every candidate: does it compose more than one tool, or encode a convention that exists only in this system? An alias over a single command does not enter. If an application owns the state, the command belongs to that application.
- Explicit non-verbs, never to be implemented: reboot, shutdown, volume, brightness, screenshot. They reach the launcher as *system actions*.
- Every verb declares whether it needs the server. Without it, fail explicitly or queue — never hang.
