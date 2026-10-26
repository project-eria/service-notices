# ERIA Project - Notices

Notifications de la maison : un seul point central qui les garde (SQLite), les donne à
l'application et les envoie aux canaux de chaque personne.

## Thing `urn:eria:service:notices`

- `raise {key, source, severity, title, body?, audience, thing?}` → id. Une notice active par
  `key` : un nouveau `raise` avec la même clé la met à jour sans la renvoyer sur les canaux.
  Défauts : `source: house`, `severity: info`, `audience: profile`.
- `resolve {key}` → `true` si une notice active a été close (elle reste dans l'historique).
- `markRead {profile, ids}` — le profil viendra du jeton quand `auth` existera.
- Propriétés : `notices` (100 dernières, actives et résolues), `read/{profile}` (ids lus).
- Events : `raised`, `resolved`.

## Canaux

`subscriptions` en config : une URL [shoutrrr](https://github.com/nicholas-fedor/shoutrrr) par
personne et par canal (ntfy, Discord, Telegram, Pushover, Gotify…), avec `minSeverity`. Les
options propres au service (priorité ntfy, lien `click`…) vont dans l'URL.

iPhone, app fermée : app ntfy abonnée au topic ; avec un ntfy auto-hébergé, régler
`upstream-base-url: https://ntfy.sh` (seul un réveil APNs passe par ntfy.sh).
