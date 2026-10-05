# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包（仅 **amd64 / x86**）。
**完全离线**：Docker 镜像随包内置（`app/images/offlineu-amd64.tar`），启动时由
`cmd/main` 执行 `docker load`，飞牛OS 全程不访问任何镜像仓库。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 元信息（platform=x86、service_port=5000、checkport=false）
│   ├── ICON.PNG / ICON_256.PNG
│   ├── app/
│   │   ├── docker/docker-compose.yaml  # 仅作手动/参考（等价于 cmd/main 的 docker run）
│   │   └── images/offlineu-amd64.tar   # 内置镜像（打包时生成，*.tar 已 gitignore）
│   ├── cmd/install_init      # 安装最早期（native 应用：无操作）
│   ├── cmd/install_callback  # 清理旧容器
│   ├── cmd/main              # ★ 核心：docker load + docker run（host 网络）
│   ├── cmd/{upgrade,uninstall}_*  # 升级/卸载清理容器
│   ├── config/privilege      # 运行身份
│   ├── config/resource       # {} —— 不声明 docker-project（native 应用）
│   └── wizard/install        # 安装向导（课程目录、端口、时区）
└── build.ps1                # 本地一键打包（Windows：构建镜像 + 导出 + fnpack）
```

## 为什么是 native 应用（而不是 docker-project）

飞牛OS 的 `docker-project` 会在**安装阶段就执行 `docker compose up`**，此时任何生命周期
钩子都还没机会 `docker load` 包内的离线镜像，于是报
`Error response from daemon: No such image: offlineu:local`，安装直接失败。

因此本包采用社区验证过的 **native 应用**形态（与 MiBee NVR 的离线包一致）：

- `config/resource` 为 `{}`，**不声明 `docker-project`**；
- `cmd/main` 自己管理容器：`start` 时先 `docker load` 内置镜像，再 `docker run -d`；
- 使用 `--network host`（容器自行绑定端口，`manifest` 设 `checkport=false`，且不写 `ports`）；
- 路径用飞牛注入的变量：`${TRIM_APPDEST}`（安装目录）、`${TRIM_PKGVAR}`（持久数据目录）。

## 打包

### 本地打包（推荐，完全离线）

`fnos/build.ps1`：① `docker build --platform linux/amd64 -t offlineu:local`（含前端 +
内置 ffmpeg）→ ② `docker save` 到 `app/images/offlineu-amd64.tar` → ③ 包内文本统一 LF
→ ④ 官方 `fnpack` 打包成 `offlineu_1.0.0_x86.fpk`。

```powershell
cd fnos
.\build.ps1            # 产物 fnos\offlineu_1.0.0_x86.fpk
```

要求：Windows 已启动 Docker Desktop（Linux 容器模式）；首次运行需联网下载 fnpack 与拉取
基础镜像（仅**构建**阶段联网，产物对 NAS 完全离线）。

### CI 打包（GitHub Actions）

`.github/workflows/docker-build.yml` 的 `fpk` job 用 `docker buildx` 构建 amd64 镜像并
`docker save` 到包内，再 `fnpack build`，产物上传为 Artifacts（`offlineu-fpk-x86`）；
打 `v*` tag 时附到 Release。CI 的 compose 已写死 `offlineu:local` + `pull_policy: never`，
安装时不依赖 ghcr。

## 安装（离线，无镜像仓库）

1. 飞牛OS → 应用中心 → 手动安装 → 上传 `offlineu_1.0.0_x86.fpk`。
2. 向导填写：
   - **课程目录（绝对路径）**：宿主机上课程文件夹的绝对路径（如 `/vol1/1000/offlineu/courses`），
     容器内挂到 `/courses`（只读）。
   - **服务端口**（默认 5000）、**时区**。
3. 安装完成后启动：`cmd/main` 自动 `docker load` 包内镜像，并以 `--network host` 启动
   容器，**无需联网**。
4. **进度数据固定写在飞牛的持久目录**（`${TRIM_PKGVAR}/data`，无需指定；升级/重启保留）。
5. 浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。

## 排错

- **`No such image: offlineu:local`**：说明走了 `docker-project`（安装阶段 compose up）。
  本包已改用 native 应用（见上）；若仍出现，检查 `config/resource` 是否为 `{}`。
- **启动日志**：`cmd/main` 把 `docker load` / `docker run` 的输出写入
  `${TRIM_TEMP_LOGFILE:-${TRIM_PKGVAR}/offlineu-main.log}`；容器日志可用
  `docker logs offlineu` 查看。
- **应用包不符合系统要求**：fpk 必须由官方 `fnpack` 生成，`manifest.platform` 须为
  `x86`，且包内脚本为 LF（本仓库用 `.gitattributes` + 构建脚本强制 LF）。
