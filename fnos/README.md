# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包（仅 **amd64 / x86**）。
**完全离线**：Docker 镜像随包内置，安装时由本地的 `cmd/main` 执行 `docker load`，
飞牛OS 全程不访问任何镜像仓库。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 应用元信息（appname/version/platform=x86/端口等）
│   ├── ICON.PNG             # 64×64 图标
│   ├── ICON_256.PNG         # 256×256 图标
│   ├── app/docker/
│   │   ├── docker-compose.yaml  # image: offlineu:local + pull_policy: never
│   │   └── offlineu-image.tar   # 本地构建出的镜像（打包时生成，*.tar 已被 gitignore）
│   ├── cmd/main             # native 生命周期入口：docker load + compose up/down/status
│   ├── cmd/install_callback # 安装时预加载内置镜像
│   ├── config/privilege     # 运行身份
│   ├── config/resource      # 共享目录声明
│   └── wizard/install       # 安装向导（端口、时区）
└── build.ps1                # 本地一键打包（Windows：构建镜像 + 导出 + fnpack）
```

## 为什么是 native 应用（不用 docker-project）

飞牛OS 的 `docker-project` 资源会在**任何生命周期钩子之前**就执行 `docker compose up`。
若镜像随包内置、靠钩子 `docker load`，compose 启动时镜像尚不存在会报 `No such image`。
因此本包声明为 **native 应用**：不写 `docker-project`，而由 `cmd/main` 自己负责
`docker load 内置镜像` → `docker compose up`，从而完全离线。

## 打包

### 本地打包（推荐，完全离线）

`fnos/build.ps1` 在本机完成：① `docker build --platform linux/amd64 -t offlineu:local`
（含前端 + 内置 ffmpeg）→ ② `docker save` 导出到 `app/docker/offlineu-image.tar`
→ ③ 把包内文本统一成 LF → ④ 官方 `fnpack` 打包成 `offlineu_1.0.0_x86.fpk`。

```powershell
cd fnos
.\build.ps1            # 产物 fnos\offlineu_1.0.0_x86.fpk
```

要求：Windows 上已启动 Docker Desktop（Linux 容器模式），且首次运行能联网下载 fnpack
与拉取基础镜像（构建阶段联网，产物对 NAS 完全离线）。

### CI 打包（GitHub Actions）

`.github/workflows/docker-build.yml` 的 `fpk` job：用 `docker buildx` 构建 amd64 镜像并
`docker save` 进包树，再用官方 `fnpack` 打包，产物上传为 Artifacts（`offlineu-fpk-x86`）；
打 `v*` tag 时还会附到对应 Release。CI 同样内置镜像，不依赖 ghcr 拉取。

> 注：`build` job 仍会把镜像推到 `ghcr.io/nickkk333/offlineu:main` 作为在线备用，但本
> fpk 的 compose 已写死 `offlineu:local` + `pull_policy: never`，安装时不会去 ghcr。

## 安装（离线，无镜像仓库）

1. 飞牛OS → 应用中心 → 手动安装 → 上传 `offlineu_1.0.0_x86.fpk`。
2. 向导中设置对外端口（默认 5000）与时区。
3. 安装/启动阶段 `cmd/main` 自动 `docker load` 包内镜像并 `compose up`，**无需联网**。
4. 把课程文件夹放进应用安装目录下的 `app/docker/courses`（只读挂载到容器内 `/courses`）：
   在飞牛「文件管理」里找到 offlineu 应用的安装目录（一般在 `/vol1/1000/apps/offlineu`
   或类似路径），于其 `app/docker/courses` 放入课程；进度数据写在 `app/docker/data`。
5. 浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。

## 常见报错

- **应用包不符合系统要求**：fpk 不是 `fnpack` 生成，或 `manifest` 的 `platform` 不是
  `x86`/`arm`，或包内脚本是 CRLF（本仓库已用 `.gitattributes` + 构建脚本强制 LF）。
- **启动后容器不存在 / No such image**：说明镜像未被 load。确认 fpk 由本仓库脚本生成
  （含 `offlineu-image.tar`），且安装时运行了 `cmd/main start`（native 应用由应用中心调用）。
