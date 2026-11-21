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

注册仅限邀请码用户。启动服务器前设置 `MAJDATA_INVITE_CODE`，并仅向获准注册的人提供该邀请码。访问 `https://your-domain.example/register` 创建账号。用户名长度为 3–32 个字符，只能包含英文字母、数字、下划线、句点或连字符；密码长度为 10–128 个字符。

MajdataPlay 的登录协议会发送所输入密码的小写 MD5 摘要。注册表单会将该协议摘要再以 Argon2id 哈希后保存，而不会直接保存 MD5 摘要。由于游戏协议中的 MD5 值等同于密码凭据，账号和成绩 API 必须通过 HTTPS 对外提供。公开部署时，请使用 Caddy 等 TLS 反向代理。

游戏可通过 `/api/account/login` 登录，通过 `/api/account/profile` 和 `/api/account/scores` 获取当前账号资料和最佳成绩，并通过 `/api/account/logout` 登出。发送到 `/api/maichart/{chartId}/score` 的成绩会保存到 SQLite。成绩列表按谱面难度保留最佳记录，并依次按 classic 达成率、DX 分数排序。默认数据库为 `data/majdata.sqlite`；Git 会忽略 `data/`。

公开的单曲排行榜可通过 `GET /api/maiscore/leaderboard/chart/by-id/{chartId}` 获取。响应中的 `scores` 包含按难度索引的八个数组；每位玩家在每个难度中仅保留 classic 达成率最高的成绩，若达成率相同则依次按 DX 分数和时间排序。排行榜会公开玩家用户名。目前没有全局综合排行榜、邮箱验证、密码找回或账号管理功能。请妥善保护邀请码和数据库备份。

## 段位课程

服务器从工作目录的 `server_config.json` 读取配置。复制 `server_config.example.json` 并填写邀请码和管理员密钥；真实配置文件已加入 Git 忽略列表。环境变量 `MAJDATA_INVITE_CODE`、`MAJDATA_DAN_ADMIN_TOKEN` 和 `MAJDATA_DB_PATH`（若设置）会覆盖对应文件值。

在 `server_config.json` 中将 `dan_admin_token` 设置为至少 32 个字符的随机密钥，然后通过 HTTPS 打开 `/admin/dan`。只有持有此密钥的服务器管理员可以创建、编辑或删除课程。页面可按标题、艺术家、谱师、谱面 ID、难度和等级筛选谱面；每首选中的歌曲都需要指定难度，也可以调整游玩顺序。课程保存在 SQLite 中，并作为 Dan 课程而非歌单提供。登录后的 `GET /api/account/favorite/collection/list` 会按游玩顺序返回一一对应的 `songHashs` 和 `songLevels` 数组；每个 `songLevels` 值是难度索引，Easy 为 `0`，UTAGE 为 `6`。已有课程需要编辑并保存一次以配置难度。每个课程最多包含 50 首歌曲。

游戏客户端必须能够反序列化并映射 `songLevels` 到段位课程模型；不支持该字段的旧版客户端仍需要更新客户端。

MajdataPlay 会通过现有的单曲排行榜提交并显示每首谱面的成绩；当前游戏客户端不支持提交或显示段位课程的汇总挑战记录。

## 收藏列表、推荐与评级

`collections/` 下包含 `playlist.json` 的每个文件夹都会定义一个只读公开收藏列表。显示名称取自 playlist 的 `name`，收藏列表 ID 根据其文件夹名称稳定生成；`levelIds` 会与谱面文件夹名（songId）匹配。若 `charts/` 中没有对应谱面，则忽略该 ID。同一谱面只保留一份物理文件，但可以出现在多个收藏列表中。服务器启动时会重新生成 `collections/manifest.json`。

主系列收藏列表按日本街机版本的发布时间排列；非系列收藏列表随后按名称排序。

启动时会删除空的 `charts/uncategorized/` 暂存文件夹。若其中有谱面文件，则保留在原处，并按其 ID 加入所有匹配的 playlist，从而保持由文件夹路径生成的谱面 ID 不变。

公开收藏列表 API 包括 `GET /api/collection/list?page=0&pageSize=30`、`GET /api/collection/{id}/songlist`（按 playlist 顺序返回谱面 ID）和 `GET /api/collection/{id}/hashlist`（返回谱面哈希）。兼容游戏的 `GET /api/account/favorite/collection/list` 会将相同定义作为歌单返回，并要求用户已登录。这些自动生成的歌单为只读，并会返回给所有已登录账号。

推荐信息流和 B30 评级尚未实现。MajdataPlay 会从 `GET /recommend/feed` 获取推荐页面，并向 `POST /recommend/impressions` 报告曝光；仅凭客户端请求无法确认推荐后端的具体算法。

已查看的 MajdataNet 评级界面描述的是 Best50 雷达图。这不足以证明其采用 B30 公式，因此本项目不会声称实现了这两种评级模型中的任何一种。

## 许可证与托管内容

请参阅 [LICENSE.txt](LICENSE.txt)。你有责任确认自己托管的谱面、音乐、图片及其他内容可以合法分发。
