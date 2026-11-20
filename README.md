# thumb 👍

Like `finger`, but for the Atmosphere. `.plan` files over the AT Protocol, in the spirit of John Carmack's old id Software plan updates.

Your plan lives in **your own AT Protocol repo** (Bluesky's PDS or one you host), so you own it. Anyone can thumb you using only your handle. Reading needs no account and no API key.

```
$ thumb simpson.example.com
Login: simpson.example.com                   Name: Homer Simpson
Last plan update: Fri Nov 20 07:58 2026 (PST)
Plan:
* ate the donuts in the break room (all of them)
* napped at the safety console. safety first, then nap
+ fixed the gutter Marge asked about in April
- jogging with Flanders

Next: Moe's.
```

`*` done, `+` done late, `-` not happening. That's the Carmack-style shorthand,
but a plan is just plain text, so write it however you like.

## Install

On macOS, Linux or FreeBSD:

```
curl -fsSL https://thumbplan.date/install.sh | sh
```

The script picks the right build for your system, checks its SHA-256 checksum, and installs it to `/usr/local/bin` (or `~/.local/bin` if that isn't writable). Builds installed this way run on macOS without any security prompt. Running it again updates thumb.

On Windows, or to install by hand, download a file from this repo's [Releases](../../releases) page and put `thumb` on your `PATH`. If you download it on a Mac with a browser, run `xattr -d com.apple.quarantine ./thumb` once, because the binaries aren't signed by Apple.

Or build it yourself with Go 1.25 or later:

```
git clone <this repo> && cd thumb
go build -o thumb .
```

## Use

| Command | What it does |
|---|---|
| `thumb alice.bsky.social` | Show their latest plan |
| `thumb -n 10 alice.bsky.social` | Show their last 10 entries, like a Carmack-style log |
| `thumb -l alice.bsky.social` | Long format: also show their DID and PDS |
| `thumb login` | Sign in with an **app password** (Bluesky > Settings > Privacy and security > App passwords) |
| `thumb edit` | Open your plan file in `$EDITOR`; when you save and quit, publish it if it changed |
| `thumb post` | Publish your plan file as it is now (after editing it in any editor you like) |
| `thumb post -m "text"` / `cat notes.txt \| thumb post` | Publish text directly, skipping the plan file |
| `--amend` (on `edit` or `post`) | Replace your latest entry instead of adding a new one |
| `thumb delete latest` (or a record key) | Remove an entry |
| `thumb serve --port 7979` | Run a real RFC 1288 finger daemon |

### Your plan file

Like the original finger, your plan is a plain text file, **`~/.plan`** by default. Keep it anywhere you like:

```
thumb edit -f ~/Documents/plan.txt          # one-off
export THUMB_PLAN=~/.thumb/.plan            # permanent (put this in ~/.zshrc)
```

Write in it whenever you like, in whatever editor you like, then run `thumb post` to publish it. Or run `thumb edit` to open it, write, save, and publish in one step. On a new computer, `thumb edit` creates the file from your latest published entry. thumb won't post a duplicate if the file matches your latest entry.

Each publish adds a new entry, so your published history is a log, the way Carmack's was. The file always holds your current plan.

### The real `finger` command

`thumb serve` runs a finger gateway. On port 79 (which needs root, or `sudo setcap cap_net_bind_service=+ep ./thumb`), the classic client just works:

```
finger alice.bsky.social@yourserver.com
```

By default the gateway only connects to public internet addresses. Strangers choose what it looks up, and their DID documents choose which servers it contacts, so without that rule they could point it at your private network. Add `--allow-private` only on a trusted LAN or for testing.

`finger` treats the last `@` as the host, so the gateway receives `alice.bsky.social` and looks that user up on the AT Protocol. On another port, use `printf 'alice.bsky.social\r\n' | nc yourserver.com 7979`. A query starting with `/W` returns the long format. Forwarding is refused, and the gateway handles at most 64 lookups at once.

## How it works

1. **Handle to DID**: try the DNS TXT record `_atproto.<handle>`, then `https://<handle>/.well-known/atproto-did`, then the public AppView as a last resort.
2. **DID to PDS**: fetch the DID document from `plc.directory` (for `did:plc`) or `/.well-known/did.json` (for `did:web`), and read its `#atproto_pds` service. Check that the document claims the handle back; if it doesn't, the output says "handle unverified".
3. **Read**: call `com.atproto.repo.listRecords` on the user's own PDS for the `date.thumbplan.plan` collection, newest first. The display name comes from their `app.bsky.actor.profile`.
4. **Write**: `createSession` with an app password, then `createRecord` / `putRecord` (guarded with `swapRecord`) / `deleteRecord`. Tokens are stored in your OS config directory (`~/.config/thumb`, `~/Library/Application Support/thumb` or `%AppData%\thumb`, mode 600), and refreshed automatically. Your password is never stored.

Each entry is a record with a TID key, so the plan is an append-only log, the way Carmack's was. The schema is in [`lexicon/date.thumbplan.plan.json`](lexicon/date.thumbplan.plan.json).

Control characters are stripped from everything shown, so a plan can't inject terminal escape codes. That was the classic finger security hole.

## Good to know

- **The record name is `date.thumbplan.plan`**: the `plan` type under [thumbplan.date](https://thumbplan.date), written backwards the way AT Protocol names are. Every copy of thumb must use the same name to read each other's plans, so don't change it casually.
- Plan records are public, like everything in an AT Protocol repo. Bluesky's app ignores collections it doesn't know about, so plans won't show up in anyone's feed.

## Releasing

```
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow runs GoReleaser. It builds all eight binaries, packages them (`.tar.gz`, or `.zip` for Windows) with checksums, and publishes a GitHub Release. To add `brew install` and `scoop install`, follow the comments at the bottom of `.goreleaser.yaml`. The `ci` workflow runs the tests on Linux, macOS and Windows for every push and pull request.

## Environment variables

`THUMB_PLAN`, `THUMB_COLLECTION`, `THUMB_APP_PASSWORD` (for scripted login), `THUMB_CONFIG_DIR`, `THUMB_APPVIEW`, `THUMB_PLC`, and `THUMB_ALLOW_HTTP_PDS=1` (allows a plain-HTTP PDS; for local test networks only, since your password and tokens would cross the network unencrypted).

## Development

```
go test ./...        # full flow against an in-memory mock PDS, plus the finger gateway
go build -o thumb .
```
