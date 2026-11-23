# The config and the register

## The config

`~/.config/hedge/config.yaml` (or `HEDGE_CONFIG`) is an estate's rules. It
lives in the estate's own toolkit, never in a repository hedge guards.

```yaml
register:
  repo: git@git.example.com:acme/hedge-register.git   # cloned and pulled
  # path: ~/hedge-register                            # or a folder
fixtures: https://github.com/orchard-suite/hedge/blob/main/docs/fixtures.md
zones:
  default: []            # a repository in no rule names nothing
  authors: [self]        # may appear in commit authors, LICENSE, manifests
  rules:
    - match: git.example.com/acme-home/*    # a prefix
      allow: [home]
    - match: git.example.com/acme/register  # one repository
      allow: [home, work]
publish:
  contributions: collaborators   # hedge publish lets only collaborators open pull requests
harvest:
  every: 24h
  dictionary: /usr/share/dict/words
  sources:
    - name: catalog
      category: work
      run: [sh, -c, "catalog-export --titles"]
      timeout: 5m
```

A repository's key is its remote without a scheme, a user, a port, or
`.git`, in lower case: `git@git.example.com:Acme/App.git` is
`git.example.com/acme/app`. A local folder is `file:` and its path. The
most specific rule wins. A push is judged by the remote's address and the
address git pushes to, and the stricter of the two.

`authors` names the categories a person's own name is in. They may appear
in a commit's author and committer, and in a project's credits (`LICENSE`,
`NOTICE`, `AUTHORS`, and package manifests), and nowhere else.

A harvest source prints one term per line. A term made only of dictionary
words, allow-list words, numbers, and short codes such as `vm2` is
dropped: it would flag ordinary prose. One source that fails keeps its
last terms.

## The register

```
register.yaml
generated/<source>.yaml
```

```yaml
salt: 3f2a...              # makes ids; never change it
categories:
  work:
    terms:
      - Globex
      - Globex Prime         # a phrase
      - {term: GBX, case: exact}
    nets: [198.51.100.0/24]
  home:
    terms: [novaline]
  self:
    terms: [Kim Example]
allow: [orbit, k8s]          # words a harvest never adds
```

At most eight categories. `hedge add` writes `register.yaml`, commits with a
message that names the category and never the term, and pushes.
`generated/` is written only by a harvest.

## Ids

A finding's id, such as `hedge-3f9a1c0e22b4`, is a salted hash of its term.
It says which term without saying the term, so it can stand in a
repository's `.hedgeignore` and in an estate's conformance facts:

```
# .hedgeignore: one waived finding per line, by id and an optional path
hedge-3f9a1c0e22b4 docs/history/**
```

Only a person adds a line, with `hedge ignore` in their own terminal; the
hook stops an agent from editing the file.
