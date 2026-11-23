# The fixture world

A test, an example, a doc, or a demo needs names. Take them from here. A
name copied from a real system, even one that looks harmless, says
something about the estate it came from. Never copy data from a real
system into a fixture: invent it, in the shape the real data has.

## Places

| Kind | Use |
| --- | --- |
| Domains | `example.com`, `example.org`, `example.net`, and their subdomains (`api.example.com`); `*.example`, `*.test`, `*.invalid` (RFC 2606) |
| Email | `ada@example.com`, `ops@example.org` |
| IPv4 | `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24` (RFC 5737); `10.0.0.0/8` only for a private network that needs one |
| IPv6 | `2001:db8::/32` (RFC 3849) |
| Hosts | `node-1`, `web-01`, `db-a`, `build-box`, `studio-mac` |
| Clusters and sites | `cluster-a`, `lab`, `east`, `west` |

## Organizations and products

| Kind | Use |
| --- | --- |
| Companies | Acme, Globex, Initech, Umbrella, Hooli |
| Forge owners | `acme`, `acme-labs`, `example` |
| Products and programs | Atlas Lite, Beacon, Comet, Delta Portal, Ember |
| Teams | Platform, Payments, Web, Data |
| Cloud accounts | `sub-dev`, `sub-prod`, resource groups `rg-app`, `rg-shared` |

## People

Ada, Bob, Cy, Dee, Eve, Fay, Gus, Hal. With a surname: Ada Example, Sam
Sample. Never a coworker, a family member, or a customer contact, by name,
initials, or user name.

## Identifiers

| Kind | Use |
| --- | --- |
| UUIDs | `00000000-0000-0000-0000-000000000001`, or one from `uuidgen` made for the fixture |
| Subscription and tenant ids | `11111111-1111-1111-1111-111111111111` |
| Keys and tokens | `test-token`, `sk-test-0000`; never a real one, even an expired one |
| Numbers that look like ids | `1001`, `4242` |
| Paths | `/home/ada`, `/srv/app`, `~/work`; never a real home folder |

## Shapes, not copies

When a test needs data shaped like a real system's (a catalog's rows, a
monitor's output, a forge's repositories), write a few rows by hand with
these names, or generate them. Keep the shape and the edge cases; drop
every value.
