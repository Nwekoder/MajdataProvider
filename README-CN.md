# Majdata Provider

Majdata Provider 是一个用 Go 编写的 HTTP 服务器，用于向 MajdataPlay 提供在线谱面。服务器会递归扫描 `charts/`，查找包含 `maidata.txt` 的文件夹，并提供谱面元数据和资源。同时，它还支持仅限邀请码注册、与游戏兼容的登录方式，以及基于 SQLite 的玩家成绩存储。

## 运行

需要 Go 1.25 或更高版本。

```powershell
$env:MAJDATA_INVITE_CODE = "请替换为足够长的随机邀请码"
go run .
```

服务器默认监听 `8080` 端口，并自动创建 `data/majdata.sqlite`。可通过 `MAJDATA_DB_PATH` 修改数据库位置。

## 添加谱面

将每个谱面放入 `charts/` 下的独立文件夹。支持任意深度；只要文件夹中含有 `maidata.txt`，服务器就会发现该谱面。

```text
charts/
  dx_circle_plus/
    chart_id/
      maidata.txt
      track.mp3
      bg.jpg
```

更改谱面后请重启服务器，以重新生成清单和内存中的目录。

## 连接 MajdataPlay

在游戏的 `settings.json` 中配置 `Online.ApiEndPoints`。URL 必须以 `/api/` 结尾：

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

谱面 API 包括 `/api/info`、`/api/maichart/list`、谱面信息和资源路由，以及 `/api/maichart/changes`。

## 账号与成绩

注册仅限邀请码用户。启动服务器前设置 `MAJDATA_INVITE_CODE`，并仅向获准注册的人提供该邀请码。通过 `POST /api/account/register` 发送包含 `username`、`password` 和 `inviteCode` 的 JSON 来创建账号。用户名长度为 3–32 个字符，只能包含英文字母、数字、下划线、句点或连字符；密码长度为 10–128 个字符。服务器不再提供 HTML 注册页面。

MajdataPlay 的登录协议会发送所输入密码的小写 MD5 摘要。注册表单会将该协议摘要再以 Argon2id 哈希后保存，而不会直接保存 MD5 摘要。由于游戏协议中的 MD5 值等同于密码凭据，账号和成绩 API 必须通过 HTTPS 对外提供。公开部署时，请使用 Caddy 等 TLS 反向代理。

客户端可通过 `/api/account/login` 登录，通过 `/api/account/profile` 读取或更新资料，通过 `/api/account/scores` 获取最佳成绩，并通过 `/api/account/logout` 登出。`GET` 和 `POST /api/account/gamesetting` 读取或保存当前用户的原始 JSON 游戏设置。发送到 `/api/maichart/{chartId}/score` 的成绩会保存到 SQLite。成绩列表按谱面难度保留最佳记录，并依次按 classic 达成率、DX 分数排序。默认数据库为 `data/majdata.sqlite`；Git 会忽略 `data/`。

公开的单曲排行榜可通过 `GET /api/maiscore/leaderboard/chart/by-id/{chartId}` 获取。响应中的 `scores` 包含按难度索引的八个数组；每位玩家在每个难度中仅保留 classic 达成率最高的成绩，若达成率相同则依次按 DX 分数和时间排序。排行榜会公开玩家用户名。目前没有全局综合排行榜、邮箱验证、密码找回或账号管理功能。请妥善保护邀请码和数据库备份。

## 收藏列表、推荐与评级

`collections/` 下包含 `playlist.json` 的每个文件夹都会定义一个只读公开收藏列表。显示名称取自 playlist 的 `name`，收藏列表 ID 根据其文件夹名称稳定生成；`levelIds` 会与谱面文件夹名（songId）匹配。若 `charts/` 中没有对应谱面，则忽略该 ID。同一谱面只保留一份物理文件，但可以出现在多个收藏列表中。服务器启动时会重新生成 `collections/manifest.json`。

收藏列表按名称排序，不依赖硬编码的系列顺序。

启动时会删除空的 `charts/uncategorized/` 暂存文件夹。若其中有谱面文件，则保留在原处，并按其 ID 加入所有匹配的 playlist，从而保持由文件夹路径生成的谱面 ID 不变。

`GET /api/collection/list` 会列出已发现的公开收藏列表和当前用户可访问的歌单。收藏列表可通过 `/api/collection/{id}/songlist` 和 `/api/collection/{id}/hashlist` 获取曲目。登录用户可通过 `/api/collection/create`、`PUT /api/collection/{id}/modify` 和 `/api/collection/{id}/destroy` 管理自己的歌单，并通过 `POST` 或 `DELETE /api/account/favorite/collection/diff` 管理收藏。由文件生成的收藏列表只读；用户歌单存储在 SQLite 中，支持公开或私有。

推荐信息流和 B30 评级尚未实现。MajdataPlay 会从 `GET /recommend/feed` 获取推荐页面，并向 `POST /recommend/impressions` 报告曝光；仅凭客户端请求无法确认推荐后端的具体算法。

已查看的 MajdataNet 评级界面描述的是 Best50 雷达图。这不足以证明其采用 B30 公式，因此本项目不会声称实现了这两种评级模型中的任何一种。

## 许可证与托管内容

请参阅 [LICENSE.txt](LICENSE.txt)。你有责任确认自己托管的谱面、音乐、图片及其他内容可以合法分发。
