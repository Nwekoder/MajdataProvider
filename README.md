# Majdata Provider

Majdata Provider is a Go HTTP server for hosting online charts for MajdataPlay. It scans `charts/` recursively for `maidata.txt` files and serves chart metadata and assets. It also provides REST account, game-setting, playlist, favorite, and score APIs backed by SQLite.

## Run

Requires Go 1.25 or later.

```powershell
Copy-Item server_config.example.json server_config.json
# Edit server_config.json and set invite_code.
go run .
```

The server reads `server_config.json` from its working directory and listens on port `8080`. It creates `data/majdata.sqlite` automatically. Environment variables `MAJDATA_INVITE_CODE` and `MAJDATA_DB_PATH`, when set, override the matching file values. The real config file is excluded from Git because it can contain secrets; do not commit or share it.

## Add charts

Put each chart in its own folder under `charts/`. Any folder depth is supported; a chart is discovered when its folder contains `maidata.txt`.

```text
charts/
  dx_circle_plus/
    chart_id/
      maidata.txt
      track.mp3
      bg.jpg
```

Restart the server after changing charts so the manifest and in-memory catalog are rebuilt.

## Connect MajdataPlay

Configure `Online.ApiEndPoints` in the game's `settings.json`. The URL must end in `/api/`:

```json
{
  "Online": {
    "Enable": true,
    "ApiEndPoints": [{
      "Name": "MyChartSource",
      "Url": "https://your-domain.example/api/"
    }]
  }
}
```

The chart API includes `/api/info`, `/api/maichart/list`, chart info and asset routes, and `/api/maichart/changes`.

## Accounts and scores

Registration is invite-only. Set `MAJDATA_INVITE_CODE` before starting the server, then give that code only to people you want to register. Create accounts with `POST /api/account/register` and a JSON body containing `username`, `password`, and `inviteCode`. Usernames are 3–32 letters, numbers, underscores, periods, or hyphens; passwords must be 10–128 characters. The former HTML registration page is not served.

MajdataPlay's login protocol sends the lowercase MD5 digest of the entered password. The registration form stores an Argon2id hash of that protocol digest, not the digest itself. Because the game protocol's MD5 value is password-equivalent, expose account and score endpoints only over HTTPS. For public deployments, put the server behind a TLS reverse proxy such as Caddy.

Clients can log in at `/api/account/login`, read or update profiles at `/api/account/profile`, read best scores at `/api/account/scores`, and log out at `/api/account/logout`. JSON login accepts the original password; the legacy game form protocol remains supported. Score submissions to `/api/maichart/{chartId}/score` are saved in SQLite. The score list keeps the best result for each chart difficulty, ordered by classic accuracy, then DX score. The database is `data/majdata.sqlite` by default; `data/` is excluded from Git.

`GET` and `POST /api/account/gamesetting` read and save the authenticated user's raw JSON game settings. `GET /api/collection/list` lists discovered public collections and accessible user playlists. Authenticated users can create playlists with `POST /api/collection/create`, edit them with `PUT /api/collection/{id}/modify`, and delete them with `POST /api/collection/{id}/destroy`. The songlist and hashlist routes return chart IDs and hashes; favorite playlists are managed with `POST` and `DELETE /api/account/favorite/collection/diff` and listed at `GET /api/account/favorite/collection/list`.

The public per-chart leaderboard is available at `GET /api/maiscore/leaderboard/chart/by-id/{chartId}`. Its eight `scores` arrays are indexed by difficulty; each player appears once per difficulty with their best classic-accuracy result, with DX score and recency breaking ties. The leaderboard exposes player usernames. There is no global aggregate leaderboard, email verification, password recovery, or account administration. Protect the invite code and database backups.

## Playlists, recommendations, and rating

Each folder under `collections/` that contains a `playlist.json` defines a public collection discovered at startup. The playlist's `name` is displayed, its folder determines the stable collection ID, and its `levelIds` are matched against chart folder names (song IDs). IDs without a chart in `charts/` are omitted. A chart stays in one physical folder and can appear in multiple collections. `collections/manifest.json` is regenerated on startup, and collections are sorted by name without a hard-coded series list.

An empty `charts/uncategorized/` staging folder is removed on startup. If it contains charts, they stay there and are included in every matching playlist; this preserves their folder-derived chart IDs.

Generated collections are read-only; user-created playlists are stored in SQLite and support private or public visibility. Playlist items retain their order and are validated against the dynamically loaded chart catalog. Favorites can reference either generated collections or visible user playlists.

Recommendation feeds and B30 ratings are not implemented. MajdataPlay requests recommendation feed pages from `GET /recommend/feed` and reports impressions to `POST /recommend/impressions`; the recommendation backend behavior is not publicly established by those client calls.

MajdataNet's inspected rating UI describes a Best50 radar. That is not evidence of a B30 formula, so this project does not claim to implement either rating model.

## License and hosted content

See [LICENSE.txt](LICENSE.txt). You are responsible for ensuring that any charts, music, images, and other content you host may legally be distributed.
