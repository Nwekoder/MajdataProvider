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

登録は招待コード制です。サーバー起動前に `MAJDATA_INVITE_CODE` を設定し、登録を許可する人にだけコードを伝えてください。`https://your-domain.example/register` を開くとアカウントを作成できます。ユーザー名は英数字、アンダースコア、ピリオド、ハイフンを使った 3～32 文字、パスワードは 10～128 文字です。

MajdataPlay のログインプロトコルは、入力されたパスワードの小文字 MD5 ダイジェストを送信します。登録フォームはこのプロトコル値そのものではなく、その値を Argon2id でハッシュして保存します。ゲームプロトコルの MD5 値はパスワードと同等の認証情報なので、アカウントおよびスコア API は必ず HTTPS 経由で公開してください。公開環境では Caddy などの TLS リバースプロキシを使用してください。

ゲームは `/api/account/login` からログインし、`/api/account/profile` と `/api/account/scores` からプロフィールとベストスコアを取得し、`/api/account/logout` からログアウトできます。`/api/maichart/{chartId}/score` に送信されたスコアは SQLite に保存されます。スコア一覧には譜面の各難易度についてベスト記録を保持し、classic 達成率、次に DX スコアの順で選びます。デフォルトのデータベースは `data/majdata.sqlite` で、`data/` は Git の対象外です。

譜面ごとの公開ランキングは `GET /api/maiscore/leaderboard/chart/by-id/{chartId}` で取得できます。`scores` は難易度インデックス順の 8 配列で、各難易度にはプレイヤーごとに classic 達成率が最も高い記録を 1 件表示します。同率の場合は DX スコア、記録日時の順で並びます。プレイヤー名は公開されます。全体ランキング、メール確認、パスワード再設定、アカウント管理機能はありません。招待コードとデータベースのバックアップを安全に管理してください。

## 段位コース

サーバーは作業ディレクトリの `server_config.json` から設定を読み込みます。`server_config.example.json` をコピーし、招待コードと管理者トークンを設定してください。実際の設定ファイルは Git の対象外です。環境変数 `MAJDATA_INVITE_CODE`、`MAJDATA_DAN_ADMIN_TOKEN`、`MAJDATA_DB_PATH` が設定されている場合は、対応するファイルの値より優先されます。

`server_config.json` の `dan_admin_token` に 32 文字以上のランダムな秘密値を設定し、HTTPS 経由で `/admin/dan` を開いてください。このトークンを持つサーバー管理者だけがコースを作成・編集・削除できます。譜面はタイトル、アーティスト、譜面制作者、譜面 ID、難易度、レベルで絞り込めます。各曲の難易度を選び、プレイ順を並べ替えてコースを保存してください。コースは SQLite に保存され、プレイリストではなく段位コースとして提供されます。ログイン後のゲーム用 `GET /api/account/favorite/collection/list` は、プレイ順に対応する `songHashs` と `songLevels` を返します。`songLevels` は Easy の `0` から UTAGE の `6` までの難易度インデックスです。既存コースは編集して一度保存し、難易度を設定してください。1 コースにつき最大 50 曲です。

ゲームクライアントは `songLevels` をデシリアライズし、段位コースのモデルにマッピングする必要があります。このフィールドに対応していない古いクライアントには、引き続きクライアント側の更新が必要です。

MajdataPlay は既存の譜面別ランキングを通じて各譜面のスコアを送信・表示します。現在のゲームクライアントは段位コース全体のプレイ記録の送信・表示には対応していません。

## プレイリスト、推薦、レーティング

`collections/` 以下で `playlist.json` を含む各フォルダーが、読み取り専用の公開コレクションになります。表示名には playlist の `name` を使い、コレクション ID はフォルダー名から安定して生成します。`levelIds` は譜面フォルダー名（songId）と照合し、`charts/` に譜面がない ID は除外します。譜面ファイルは物理的に一つだけ保持し、複数のコレクションに掲載できます。起動時に `collections/manifest.json` を再生成します。

maimai のメインシリーズは日本国内のアーケード稼働開始日順に並び、シリーズ以外のコレクションはその後に名前順で並びます。

空の `charts/uncategorized/` ステージングフォルダーは起動時に削除します。譜面が含まれる場合はその場所に残し、ID が一致するすべての playlist に登録します。これにより、フォルダーパス由来の譜面 ID を維持します。

公開コレクション API は `GET /api/collection/list?page=0&pageSize=30`、`GET /api/collection/{id}/songlist`（playlist 順の譜面 ID）、`GET /api/collection/{id}/hashlist`（譜面ハッシュ）を提供します。ゲーム互換の `GET /api/account/favorite/collection/list` は同じ定義をプレイリストとして返し、ログインが必要です。生成されたプレイリストは読み取り専用で、ログイン中のすべてのアカウントに返されます。

推薦フィードと B30 レーティングは未実装です。MajdataPlay は `GET /recommend/feed` で推薦ページを取得し、`POST /recommend/impressions` で表示を報告しますが、クライアントのリクエストだけでは推薦バックエンドの具体的な動作は確認できません。

確認した MajdataNet のレーティング画面には Best50 レーダーと記載されています。これは B30 の計算式を示すものではないため、本プロジェクトではいずれのレーティング方式も実装済みとはしていません。

## ライセンスと配信コンテンツ

[LICENSE.txt](LICENSE.txt) を参照してください。配信する譜面、音楽、画像、その他のコンテンツを合法的に配布できることは、利用者の責任です。
