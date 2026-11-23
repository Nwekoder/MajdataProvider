# Majdata Provider

Majdata Provider は、MajdataPlay 向けにオンライン譜面を配信する Go 製 HTTP サーバーです。`charts/` を再帰的に検索し、`maidata.txt` を含むフォルダーを見つけて、譜面のメタデータとアセットを配信します。また、招待コード制のアカウント登録、ゲーム互換のログイン、SQLite によるプレイヤーのスコア保存にも対応しています。

## 起動方法

Go 1.25 以降が必要です。

```powershell
$env:MAJDATA_INVITE_CODE = "長くランダムな招待コードに置き換えてください"
go run .
```

サーバーはポート `8080` で待ち受け、`data/majdata.sqlite` を自動作成します。`MAJDATA_DB_PATH` でデータベースの場所を変更できます。

## 譜面の追加

各譜面を `charts/` 以下の個別フォルダーに配置してください。フォルダーの深さに制限はありません。フォルダー内に `maidata.txt` があれば譜面として認識されます。

```text
charts/
  dx_circle_plus/
    chart_id/
      maidata.txt
      track.mp3
      bg.jpg
```

譜面を変更した後は、manifest とメモリ上のカタログを再構築するためサーバーを再起動してください。

## MajdataPlay への接続

ゲームの `settings.json` で `Online.ApiEndPoints` を設定します。URL の末尾には `/api/` が必要です。

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

譜面 API には `/api/info`、`/api/maichart/list`、譜面情報・アセットの各ルート、`/api/maichart/changes` があります。

## アカウントとスコア

登録は招待コード制です。サーバー起動前に `MAJDATA_INVITE_CODE` を設定し、登録を許可する人にだけコードを伝えてください。`username`、`password`、`inviteCode` を含む JSON を `POST /api/account/register` に送信してアカウントを作成します。ユーザー名は英数字、アンダースコア、ピリオド、ハイフンを使った 3～32 文字、パスワードは 10～128 文字です。HTML 登録ページは提供されません。

MajdataPlay のログインプロトコルは、入力されたパスワードの小文字 MD5 ダイジェストを送信します。登録フォームはこのプロトコル値そのものではなく、その値を Argon2id でハッシュして保存します。ゲームプロトコルの MD5 値はパスワードと同等の認証情報なので、アカウントおよびスコア API は必ず HTTPS 経由で公開してください。公開環境では Caddy などの TLS リバースプロキシを使用してください。

クライアントは `/api/account/login` からログインし、`/api/account/profile` からプロフィールの取得・更新、`/api/account/scores` からベストスコアの取得、`/api/account/logout` からログアウトができます。`GET` と `POST /api/account/gamesetting` は、ログイン中ユーザーのゲーム設定 JSON を読み書きします。`/api/maichart/{chartId}/score` に送信されたスコアは SQLite に保存されます。スコア一覧には譜面の各難易度についてベスト記録を保持し、classic 達成率、次に DX スコアの順で選びます。デフォルトのデータベースは `data/majdata.sqlite` で、`data/` は Git の対象外です。

譜面ごとの公開ランキングは `GET /api/maiscore/leaderboard/chart/by-id/{chartId}` で取得できます。`scores` は難易度インデックス順の 8 配列で、各難易度にはプレイヤーごとに classic 達成率が最も高い記録を 1 件表示します。同率の場合は DX スコア、記録日時の順で並びます。プレイヤー名は公開されます。全体ランキング、メール確認、パスワード再設定、アカウント管理機能はありません。招待コードとデータベースのバックアップを安全に管理してください。

## プレイリスト、推薦、レーティング

`collections/` 以下で `playlist.json` を含む各フォルダーが、読み取り専用の公開コレクションになります。表示名には playlist の `name` を使い、コレクション ID はフォルダー名から安定して生成します。`levelIds` は譜面フォルダー名（songId）と照合し、`charts/` に譜面がない ID は除外します。譜面ファイルは物理的に一つだけ保持し、複数のコレクションに掲載できます。起動時に `collections/manifest.json` を再生成します。

コレクションは、シリーズのハードコードされた順序を使わず名前順に並びます。

空の `charts/uncategorized/` ステージングフォルダーは起動時に削除します。譜面が含まれる場合はその場所に残し、ID が一致するすべての playlist に登録します。これにより、フォルダーパス由来の譜面 ID を維持します。

`GET /api/collection/list` は検出された公開コレクションと、現在のユーザーがアクセスできるプレイリストを返します。項目は `/api/collection/{id}/songlist` と `/api/collection/{id}/hashlist` で取得できます。ログインユーザーは `/api/collection/create`、`PUT /api/collection/{id}/modify`、`/api/collection/{id}/destroy` で自分のプレイリストを管理し、`POST` または `DELETE /api/account/favorite/collection/diff` でお気に入りを管理できます。ファイルから生成されたコレクションは読み取り専用で、ユーザー作成プレイリストは SQLite に保存され、公開・非公開を選べます。

推薦フィードと B30 レーティングは未実装です。MajdataPlay は `GET /recommend/feed` で推薦ページを取得し、`POST /recommend/impressions` で表示を報告しますが、クライアントのリクエストだけでは推薦バックエンドの具体的な動作は確認できません。

確認した MajdataNet のレーティング画面には Best50 レーダーと記載されています。これは B30 の計算式を示すものではないため、本プロジェクトではいずれのレーティング方式も実装済みとはしていません。

## ライセンスと配信コンテンツ

[LICENSE.txt](LICENSE.txt) を参照してください。配信する譜面、音楽、画像、その他のコンテンツを合法的に配布できることは、利用者の責任です。
