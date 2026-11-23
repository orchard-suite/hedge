# hedge

hedge keeps an estate's own names out of repositories that may not hold
them. An estate is one organization's whole platform: its hosts, its
people, its customers, its cloud accounts. A tool that many estates use
should name none of them, and a home repository should never name work.

hedge checks every write an agent makes, every commit, and every push,
against a private register of terms. A repository says nothing about what
is forbidden: the register lives outside it, and a finding in a repository
shows only an opaque id.

## How it works

- **Zones.** Every repository is in a zone, by its remote: which
  categories of term it may name. A repository in no zone may name none.
  It is estate-neutral, the strictest zone, so a new repository is guarded
  from its first commit.
- **The register.** A private folder, usually its own private repository,
  holds the terms by category: hand-kept ones (`hedge add`), and harvested
  ones that `hedge refresh` reads from the estate's own systems, such as
  its catalog and its cloud accounts. It is compiled into one index file.
- **The gates.**
  1. A Claude Code hook stops a write or an edit that names a term its
     repository may not, a shell command that would turn the guard off, a
     repository made public around `hedge publish`, and an issue, pull
     request, or release that names a term.
  2. Git hooks, set in the global config, check each commit's added lines,
     paths, message, and author. Git runs them beside a repository's own
     hooks, so a repository that sets `core.hooksPath` cannot hide them.
  3. The pre-push hook checks every commit and tag a push sends, in the
     zone of where it goes. A home repository's history pushed to a public
     remote is stopped, though every commit was fine where it was made.
  4. `hedge publish` makes a repository public only after it checks the
     remote's whole history and its issues, pull requests, and releases.
  5. `hedge audit --facts` prints one fact per repository for an estate's
     conformance judge, so a term that arrives some other way (a web edit,
     another machine) becomes a finding.

## Speed

The hooks run on every write, so the index is built for it: one file,
mapped into memory, read without decoding. Text is cut into word parts and
each run of parts is one hash lookup; there is no regular expression and no
allocation per word. Scanning runs at about 190 MB/s. On an Apple M4, with
a register of about 2,400 terms, a hook call takes about 2 ms from start to
exit, an edit of a 100 KB file about 3 ms. Starting the process is most of
that.

## Matching

A term matches whole word parts: "acme" is found in "AcmeWidget",
"acme-widget", and "acme_widget", and not in "acmes". A term with a space
matches across spaces; one without never does, so "nova line" is not
"novaline". A term may be exact case, for a short code. An encoded blob, a
lock file, and a binary file hold no words. A register's IPv4 networks
match any address in them.

## Install

```bash
make            # build and install ~/.local/bin/hedge
hedge install   # set the git hooks for every repository
hedge refresh   # pull the register and build the index
hedge doctor    # prove a commit, a push, and a write are stopped
```

The Claude Code hook ships in this repository as the `hedge` plugin
(`hooks/hooks.json`).

## Configure

An estate keeps its rules in `~/.config/hedge/config.yaml`; the register
holds its terms. [docs/register.md](docs/register.md) has both formats.
[docs/fixtures.md](docs/fixtures.md) is the made-up world to use instead:
the names, addresses, and ids a test or an example should use.

## Commands

| Command | What it does |
| --- | --- |
| `hedge claude` | The Claude Code hook. Reads the tool call on stdin |
| `hedge git pre-commit \| commit-msg \| pre-push` | The git hooks |
| `hedge check <file>...` | Check files in their repositories' zones |
| `hedge audit [--history] [--opaque] [--facts]` | Check a working tree, and every commit on every ref |
| `hedge audit --facts --mirrors <dir> <url>...` | Keep mirrors and print one fact per repository |
| `hedge publish <owner/name>` | Check a remote's whole history, then make it public |
| `hedge zone [path]` | Say what a repository may name |
| `hedge refresh [--harvest]` | Pull the register, harvest when due, build the index |
| `hedge add <category> <term> [--exact]` | Add a term |
| `hedge ignore <id> [glob]` | Waive one finding, from a person's own terminal |
| `hedge install` | Set the git hooks |
| `hedge doctor` | Prove the guard works on this machine |

## License

MIT.
