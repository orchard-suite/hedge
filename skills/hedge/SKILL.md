---
name: hedge
description: Keep an estate's own names out of repositories that may not hold them. Use when hedge stops a write, an edit, a commit, a push, or a post ("hedge:" in a hook's message); when a test, example, or doc needs made-up names; when a person names something that must never appear outside its estate; when a repository should go public; or when asked which estate a repository may name.
---

# hedge

hedge guards each repository by its zone: the categories of term its
remote may name. Most repositories are estate-neutral and name none. The
terms are in a private register; a repository holds only opaque ids.

## When hedge stops you

The message names each term, where it is, and what the repository may
name.

1. Replace each term with a made-up name from the fixture world (the
   message links it), or leave the detail out. Keep the shape of real data,
   never its values.
2. Write again. Never step around the check: no `--no-verify`, no `-n`, no
   other repository, no other tool to write the same text. The hooks stop
   those too.
3. When the finding is wrong (the word is ordinary in this repository),
   tell the person its id and path. They waive it with
   `hedge ignore <id> [path]` in their own terminal. Never edit
   `.hedgeignore`.

## When a person names something to guard

`hedge add <category> <term>` adds it to the register and every hook on
the machine knows it at once. `--exact` for a short code whose case
matters. `hedge zone` says what the repository here may name.

## Going public

`hedge publish <owner/name>` checks the remote's whole history, every ref
and tag, and its issues, pull requests, and releases, then makes it
public. It is the only way the hooks let a repository go public.

## Checking

- `hedge audit` checks the working tree; `--history` adds every commit.
- `hedge doctor` proves a commit, a push, and a write are stopped here.
- `hedge refresh` pulls the register and rebuilds the index. With no index,
  every write into a repository waits for it.
