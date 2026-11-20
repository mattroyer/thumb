# Security

## Reporting a problem

Please report security issues privately through GitHub: on this repo, go to **Security > Report a vulnerability**. Please don't open a public issue for them.

## What thumb defends against

thumb talks to servers it doesn't control, chosen by whoever owns the handle you look up. It treats everything it receives as hostile:

- **Terminal tricks.** Plan text, names, timestamps and remote error messages all pass through `sanitize()` before printing. It strips control characters (escape sequences, bells) and invisible text-direction characters, which could otherwise rewrite your terminal or disguise text.
- **Identity spoofing.** A handle is shown only when both directions agree: the handle points to the DID, and the DID's document claims the handle back. Otherwise thumb shows exactly what you typed, marked "(handle unverified)". `thumb login` refuses to send a password to an unverified handle's server.
- **Downgrades.** A PDS must use HTTPS (override with `THUMB_ALLOW_HTTP_PDS=1` for local test networks only), and thumb never follows a redirect from HTTPS to plain HTTP.
- **Malformed identifiers.** DIDs must be well-formed `did:plc` or host-only `did:web` before thumb turns them into URLs. DID documents must describe the DID that was requested.
- **Gateway abuse (`thumb serve`).** By default the finger gateway only connects to public internet addresses. The address is checked at connect time, so redirects and DNS tricks can't reach loopback, private, link-local or other internal ranges. `--allow-private` turns this off for trusted networks.

## What's stored locally

Your login tokens (never your password) are kept in your OS config folder with owner-only permissions (`0600`). Anyone who can read that file can post as you until the tokens expire, so treat it like an SSH key. `thumb logout` revokes the session and deletes the file. App passwords can be revoked anytime in your account settings.

## Known limits

- The installer's checksum file comes from the same GitHub release as the binary. It catches corrupted or truncated downloads, but not a compromised GitHub account.
- The finger gateway caps concurrent lookups at 64 but has no per-client rate limit. Each query can trigger a few outbound requests, so put it behind a firewall or rate limiter if it's busy.
- Plans are public by design. Everything in an AT Protocol repo can be read by anyone.
