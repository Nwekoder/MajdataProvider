# Majdata Provider

Majdata Provider is a Go HTTP server for hosting online charts for MajdataPlay. It scans `charts/` recursively for `maidata.txt` files and serves chart metadata and assets. It also provides invite-only account registration, game-compatible login, and SQLite-backed player scores.

## Run

Requires Go 1.25 or later.

```powershell
Copy-Item server_config.example.json server_config.json
# Edit server_config.json and set invite_code and dan_admin_token.
go run .
```

The server reads `server_config.json` from its working directory and listens on port `8080`. It creates `data/majdata.sqlite` automatically. Environment variables `MAJDATA_INVITE_CODE`, `MAJDATA_DAN_ADMIN_TOKEN`, and `MAJDATA_DB_PATH`, when set, override the matching file values. The real config file is excluded from Git because it can contain secrets; do not commit or share it.

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

Registration is invite-only. Set `MAJDATA_INVITE_CODE` before starting the server, then give that code only to people you want to register. Open `https://your-domain.example/register` to create an account. Usernames are 3–32 letters, numbers, underscores, periods, or hyphens; passwords must be 10–128 characters.

MajdataPlay's login protocol sends the lowercase MD5 digest of the entered password. The registration form stores an Argon2id hash of that protocol digest, not the digest itself. Because the game protocol's MD5 value is password-equivalent, expose account and score endpoints only over HTTPS. For public deployments, put the server behind a TLS reverse proxy such as Caddy.

The game can log in at `/api/account/login`, read the signed-in profile and best scores at `/api/account/profile` and `/api/account/scores`, and log out at `/api/account/logout`. Score submissions to `/api/maichart/{chartId}/score` are saved in SQLite. The score list keeps the best result for each chart difficulty, ordered by classic accuracy, then DX score. The database is `data/majdata.sqlite` by default; `data/` is excluded from Git.

The public per-chart leaderboard is available at `GET /api/maiscore/leaderboard/chart/by-id/{chartId}`. Its eight `scores` arrays are indexed by difficulty; each player appears once per difficulty with their best classic-accuracy result, with DX score and recency breaking ties. The leaderboard exposes player usernames. There is no global aggregate leaderboard, email verification, password recovery, or account administration. Protect the invite code and database backups.

## Dan courses

Set `dan_admin_token` in `server_config.json` to a random secret of at least 32 characters and open `/admin/dan` over HTTPS. Only someone with this token can create, edit, or delete courses. The page filters charts by title, artist, designer, chart ID, difficulty, and level; select a difficulty for each song and reorder the course. Courses are stored in SQLite and served as Dan courses, not playlists. The signed-in `GET /api/account/favorite/collection/list` response includes matching `songHashs` and `songLevels` arrays in play order; each level is a difficulty index from Easy (`0`) through UTAGE (`6`). Existing courses need to be edited and saved once to configure their difficulties. A course can contain up to 50 songs.

The client must deserialize and map `songLevels` to its Dan course model; older client builds that omit this field still require a client update.

MajdataPlay submits and shows individual chart scores through the existing chart leaderboard. The current game client does not submit or display aggregate Dan-course attempts or results.

## Playlists, recommendations, and rating

Each folder under `collections/` that contains a `playlist.json` defines one read-only public collection. The playlist's `name` is displayed, its folder determines the stable collection ID, and its `levelIds` are matched against the chart folder names (song IDs). IDs without a chart in `charts/` are omitted. A chart stays in one physical folder and can appear in multiple collections. `collections/manifest.json` is regenerated on startup.

Mainline maimai series collections are ordered by their Japanese arcade release date; non-series collections follow them alphabetically.

An empty `charts/uncategorized/` staging folder is removed on startup. If it contains charts, they stay there and are included in every matching playlist; this preserves their folder-derived chart IDs.

The public collection API provides `GET /api/collection/list?page=0&pageSize=30`, `GET /api/collection/{id}/songlist` (chart IDs in playlist order), and `GET /api/collection/{id}/hashlist` (chart hashes). The game-compatible `GET /api/account/favorite/collection/list` returns the same definitions as playlists and requires a signed-in account. These generated playlists are read-only and are returned for every signed-in account.

Recommendation feeds and B30 ratings are not implemented. MajdataPlay requests recommendation feed pages from `GET /recommend/feed` and reports impressions to `POST /recommend/impressions`; the recommendation backend behavior is not publicly established by those client calls.

MajdataNet's inspected rating UI describes a Best50 radar. That is not evidence of a B30 formula, so this project does not claim to implement either rating model.

## License and hosted content

See [LICENSE.txt](LICENSE.txt). You are responsible for ensuring that any charts, music, images, and other content you host may legally be distributed.
