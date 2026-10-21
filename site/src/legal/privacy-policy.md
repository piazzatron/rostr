# Privacy Policy

*Last updated: October 21, 2026*

rostr is run by Rosebud Labs LLC ("we"). This policy covers the getrostr.sh website and the rostr cloud API at api.getrostr.sh (together, "the Service"). The rostr CLI in local mode runs entirely on your machine and sends us nothing.

## What we collect

rostr has no accounts. We don't ask for your name, email, or payment details. In cloud mode, the CLI sends us:

| Data | How we store it |
| --- | --- |
| Auth token (derived from your rostr key) | SHA-256 hash only. Your rostr key itself never leaves your machine. |
| Rosters (names, members, emoji) and settings | Plaintext |
| Drafts: which member, session ID, timestamps | Plaintext |
| Tasks (what an agent is working on) | Encrypted on your machine (AES-256-GCM). We can't read them. |
| Repo names (`owner/repo`) | Encrypted on your machine, plus a keyed fingerprint. We can't read them. |

We never receive your prompts, code, or files. [How the encryption works](/privacy).

Like most web services, our hosting providers may log technical data such as IP addresses and request times. We use your IP address in memory to rate-limit key creation and don't store it.

The website sets no cookies. It uses Umami, a cookieless analytics service, to count page views; Umami records anonymous, aggregate data like the pages visited, the referring site, and the browser and country, and doesn't identify you.

## How we use it

We use this data only to run the Service: storing your rosters, drafting names, and keeping your machines in sync. We don't sell it, share it for advertising, or use it to train models.

## Who processes it

We use these providers to host the Service:

- **Railway**: API and database
- **Vercel**: website
- **Umami**: website analytics
- **Cloudflare**: DNS

We may also disclose data if the law requires it. Because tasks and repo names are encrypted with a key we never have, we can't disclose their contents.

## Retention and deletion

- Released drafts are deleted after 7 days.
- Disbanding a roster (`rostr disband`) deletes it, its drafts, and its project links from our servers right away.
- Disbanding your last roster also deletes your token hash and settings, so nothing of yours is left.
- You can also email us your auth token (`rostr config key --token`), and we'll delete everything tied to it within 30 days.

If you lose your rostr key, we can't recover your data or prove it's yours, so we can't act on deletion requests without the token.

## Security

We encrypt connections with TLS and store only hashes of auth tokens. Tasks and repo names are encrypted end-to-end. No system is perfectly secure, though, so keep your rostr key private and never commit it.

## Your rights

Depending on where you live, you may have rights to access, correct, or delete your data. Email us to exercise them. Your rostr key already gives you full access through the CLI and API.

## Children

The Service isn't directed at children under 13, and we don't knowingly collect their data.

## Changes

If we change this policy, we'll update the date above. For material changes, we'll also note them on the site.

## Contact

[hello@getrostr.sh](mailto:hello@getrostr.sh)
