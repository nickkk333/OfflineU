# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 应用元信息（appname/version/端口等）
│   ├── ICON.PNG             # 64×64 图标
│   ├── ICON_256.PNG         # 256×256 图标
│   ├── app/docker/docker-compose.yaml  # 实际运行的容器（本地镜像 offlineu:local）
│   ├── cmd/main             # 生命周期入口（start/stop/status）
│   ├── config/privilege     # 运行身份
│   ├── config/resource      # 共享目录声明
│   └── wizard/install       # 安装向导（端口、时区）
├── offlineu-1.0.0.fpk       # 已构建的安装包
├── offlineu-1.0.0-image.tar # 本地构建出的镜像（需先导入 fnOS）
├── build.ps1                # 仅打包 fpk（镜像需已存在）
└── build-local.ps1          # 一键本地构建：镜像 + 导出 + 打包 fpk
```

## 本地构建（不依赖 GitHub / CI / 任何镜像仓库）

全程在本机完成，`docker build` 出镜像后打本地 tag `offlineu:local`，compose 用
`pull_policy: never` 引用它，因此安装时**不会联网拉取**任何镜像。

```powershell
cd fnos
.\build-local.ps1
```

脚本依次执行：

1. `docker build --platform linux/amd64 -t offlineu:local -f Dockerfile .`（含前端构建 + 内置 ffmpeg）。
2. `docker save offlineu:local -o offlineu-1.0.0-image.tar`（导出镜像，供 fnOS 离线导入）。
3. 用 `fnpack build` 打包 `fnos/offlineu/` 为 `offlineu-1.0.0.fpk`（无 `fnpack` 时退化为 tar.gz）。

要求本机已安装并启动 Docker（Windows 上即 Docker Desktop，且处于 Linux 容器模式）。

若只想重新打包 fpk（镜像已存在），可单独运行 `.\build.ps1`。

## 安装

1. 飞牛OS → 镜像 → 导入 → 选择 `fnos\offlineu-1.0.0-image.tar`，导入后得到镜像 `offlineu:local`。
2. 飞牛OS → 应用中心 → 手动安装 → 上传 `fnos\offlineu-1.0.0.fpk`。
3. 向导中设置对外端口（默认 5000）与时区。
4. 安装完成后，把课程文件夹放入应用对应的 `courses` 共享目录（只读挂载到容器内 `/courses`），
   浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。

> 注意：compose 写的是 `image: offlineu:local` 且 `pull_policy: never`，**必须先在步骤 1 导入
> 镜像**，否则安装会报找不到镜像。若改用在线镜像，把 compose 改回 `ghcr.io/nickkk333/offlineu:main`
> 并设 `pull_policy: always` 即可。
