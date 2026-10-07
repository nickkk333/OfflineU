// Hand-rolled i18n: two plain dictionaries, one reactive locale and a `t()`
// helper. Components call `t('area.key', { name: 'value' })` straight from their
// templates; because `t` reads the reactive `locale` ref, every component
// re-renders by itself as soon as the language is switched.
import { ref } from 'vue'

const LOCALE_KEY = 'offlineu.locale'

// Languages offered by the switch in the header. `short` is what the button
// shows for the language you would switch to.
export const LOCALES = [
  { code: 'en', label: 'English', short: 'EN' },
  { code: 'zh', label: '中文', short: '中文' }
]

const en = {
  app: {
    tagline: 'Self-hosted offline course viewer & progress tracker'
  },
  lang: {
    switchTo: 'Switch to {label}'
  },
  common: {
    loading: 'Loading…',
    refresh: '⟳ Refresh',
    up: '↑ Up',
    tryAgain: 'Try again',
    checkAgain: '⟳ Check again',
    open: 'Open',
    loadCourse: 'Load course',
    remove: 'Remove the course (deletes its progress)',
    cancel: 'Cancel',
    backToDashboard: 'Back to the dashboard',
    backToCourse: '← Back to course',
    previous: '← Previous',
    next: 'Next →',
    goTo: 'Go back to {label}'
  },
  home: {
    loadingLibrary: 'Loading your library…'
  },
  picker: {
    noticeTitle: '🔌 No course folder is mapped yet',
    noticeText:
      'OfflineU serves what you mount into it. Map your course folder to /courses (read-only) and a writable folder to /app/data — the image declares both, so NAS apps such as 飞牛OS list them while you create the container. Nothing has to be rebuilt or re-uploaded.',
    noticeFooterBefore:
      'Mounted a folder already? Make sure it contains videos, audio or documents, and that the path after the colon matches ',
    noticeFooterAfter: '.',
    noticeTitleEmpty: '📂 {root} is mapped, but still empty',
    noticeTextEmpty:
      'The folder is there and readable, it simply holds no file yet (not even nested ones). Copy your course material in — the mapped host folder in Docker, the folder behind OFFLINEU_ROOTS otherwise — and press “Check again”. Nothing has to be rebuilt or restarted.',
    noticeTitleVolume: '🔌 Docker put a throwaway volume on {root} instead of your folder',
    noticeTextVolume:
      'No host folder is bound to {root}: Docker attached a volume of its own. Your courses stay invisible here, and anything OfflineU writes there is lost as soon as the container is recreated.',
    noticeTitleNotMounted: '🔌 Nothing is mounted on {root} inside the container',
    noticeTextNotMounted:
      'There is no bind mount and no volume at {root}, so the mapping never arrived — in a container the container path has to be exactly {root}; on a local run the configured folder is simply empty.',
    noticeTitleUnreadable: '🔒 {root} is mapped, but the container cannot read it',
    noticeTextUnreadable:
      'OfflineU runs as the unprivileged user uid 10001, so a host folder that only root (or another NAS user) may read stays invisible to it even though the mapping is correct.',
    noticeTitleMissing: '📍 The mapping did not arrive: {root} is missing in the container',
    noticeTextMissing:
      'There is no folder at {root} inside the container any more; recreate the container so the mount point exists again.',
    mountStatusTitle: '📌 Mapping status',
    mountStatusOk: 'mapped, {count} entries',
    mountStatusEmpty: 'mapped, but empty',
    mountStatusNotMounted: 'nothing mounted here',
    mountStatusVolume: 'a Docker volume, not your folder',
    mountStatusUnreadable: 'mapped, but not readable by the container',
    mountStatusMissing: 'not found inside the container',
    mappingFixFolders:
      'Open the container settings of the NAS Docker app (容器 → 设置 → 文件夹) and check both columns: the host folder on the left, /courses resp. /app/data on the right.',
    mappingFixRecreate: 'Save and let the app recreate the container — a running container does not pick up a new mapping.',
    emptyFixCopy: 'Copy your course files into the host folder you mapped.',
    emptyFixRecheck: 'Come back to this page and press “Check again” — no restart is needed.',
    emptyFixBrowse: 'Or use the browser below: a folder that holds videos, audio or documents can be loaded straight away.',
    unreadableFixChmod: 'On the NAS, make the folder readable: chmod -R a+rX “<host folder>” (in fnOS, allow read access for everyone).',
    unreadableFixOwner:
      'Better: run the container as that folder’s owner. fnOS’s first user is normally 1000:1000 (check with id on the NAS) — use the 用户/User (run as) field of the container dialog if it has one, or compose: user: "1000:1000".',
    unreadableFixRoot:
      'Only if the permissions cannot be changed: run the container as root (--user 0:0, dialog user field “root”). A container cannot gain root by itself — the user is fixed when it is created and docker update cannot change it later.',
    unreadableFixData:
      'When you switch to another uid, make sure the mapped data folder is writable for it too (on fnOS /vol1/1000/offlineu-data belongs to 1000).',
    unreadableFixNoCaps:
      'This image reads any mapped folder through a built-in capability (CAP_DAC_OVERRIDE), but this container switched it off (no-new-privileges, --cap-drop DAC_OVERRIDE or --cap-drop ALL) — recreate the container without that option, or use one of the fixes above.',
    recentTitle: '🕘 Recent courses',
    recentHint: 'Your previously opened folders are remembered, so you can switch back with one click.',
    recentProgressTitle: '{completed} of {total} lessons completed',
    removeConfirmTitle: 'Remove this course?',
    removeConfirmText:
      '“{name}” will be dropped from the recent list and its saved progress deleted — re-opening the folder starts from zero. This cannot be undone.',
    removeConfirmAction: 'Remove & clear progress',
    selectTitle: '📂 Select a course',
    selectHint:
      'Browse to the folder that contains your course and load it. Videos, audio, documents and quizzes are detected automatically.',
    manualPlaceholder: '…or paste a folder path, e.g. D:\\Courses\\Python Tutorial',
    howToTitle: '🚀 How to use',
    howTo: {
      prepareLabel: 'Prepare your files',
      prepareText: 'in a folder structure (one folder per section works great).',
      browseLabel: 'Browse',
      browseText: 'to the course folder or paste its path above.',
      loadLabel: 'Load it',
      loadText: 'and start learning — progress is saved automatically.',
      comeBackLabel: 'Come back anytime',
      comeBackText: ': courses are remembered and the last one reopens after a restart.',
      autoplayLabel: 'Playback modes',
      autoplayText:
        ': choose what a finished lesson does — stop, loop the current one or roll into the next; your speed carries over.'
    },
    typesTitle: '🗂️ Supported file types',
    types: {
      videoLabel: 'Video',
      videoText: '— .mp4, .mkv, .avi, .mov, .webm, .m4v, .flv, .wmv',
      audioLabel: 'Audio',
      audioText: '— .mp3, .wav, .m4a, .aac, .ogg, .flac',
      docsLabel: 'Documents',
      docsText: '— .txt, .md, .html, .pdf, .docx, .doc, .rtf',
      subsLabel: 'Subtitles',
      subsText: '— .srt, .vtt, .ass, .sub, .sbv (converted to WebVTT on the fly)',
      quizLabel: 'Quizzes',
      quizText: '— any document whose name contains “quiz”, “exam” or “test”'
    },
    shortcuts: 'Keyboard shortcuts in a lesson: space play/pause, ← → skip 10 s, ↑ ↓ volume.'
  },
  browser: {
    listing: 'Listing folders…',
    emptyNeedsMount: 'Nothing is mounted in this folder yet — see the notice above.',
    emptyMapped: 'This folder is mapped, but there is nothing inside it yet — copy your course files into the host folder.',
    empty: 'No sub-folders here. Use this folder as the course, or go up one level.',
    mediaCount: '{count} media',
    courseBadge: 'course',
    useAsCourse: 'Use as course',
    useCurrent: 'Use this folder as course',
    actionsHint: 'Folders that contain videos or audio are highlighted and can be loaded directly.'
  },
  dash: {
    changeCourse: '⇄ Select different course',
    progressTitle: '📈 Your progress',
    chipLessons: 'lessons',
    chipCompleted: 'completed',
    chipRemaining: 'remaining',
    resumeLabel: 'Continue where you left off',
    resumeButton: '▶ Resume lesson',
    emptyLabel: 'Nothing started yet',
    emptyTitle: 'Pick a lesson below to begin',
    emptyText: 'Your position is remembered automatically, even after a restart.',
    contentTitle: '🧭 Course content',
    searchPlaceholder: 'Search lessons…',
    emptyContent: 'This course has no supported files yet.'
  },
  tree: {
    items: '{count} items',
    completed: 'Completed',
    notCompleted: 'Not completed yet'
  },
  progress: {
    lessonsCompleted: 'lessons completed'
  },
  types: {
    video: 'Video',
    audio: 'Audio',
    quiz: 'Quiz',
    text: 'Document'
  },
  lesson: {
    loading: 'Loading lesson…',
    markCompleted: 'Mark as completed',
    completed: 'Completed ✓',
    watched: 'Watched',
    resumeAt: 'Resume at',
    progressWarning: 'Progress could not be saved.',
    speed: 'Speed',
    whenFinished: 'When finished:',
    modeLoop: 'Loop',
    modeOnce: 'Once',
    modeNext: 'Next',
    modeLoopHint: 'Loop this lesson when it ends',
    modeOnceHint: 'Stop when the lesson ends',
    modeNextHint: 'Continue with the next lesson',
    loopHint: 'This lesson repeats until you switch the mode',
    upNext: 'Up next:',
    lastLesson: 'This is the last lesson of the course',
    subtitles: 'Subtitles',
    subtitleSent: 'Subtitles are sent to the TV with the cast',
    contentTitle: '📄 Content',
    loadingResource: 'Loading…',
    cannotPreview:
      'This file type cannot be previewed in the browser. Use the link below to open it with the matching desktop application.',
    openInNewTab: '📎 Open {name} in a new tab',
    shortcuts: 'Shortcuts: space play/pause · ← → skip 10 s · ↑ ↓ volume'
  },
  cast: {
    button: '📺 Cast',
    title: 'Cast to a device',
    refresh: '⟳ Search again',
    scanning: 'Searching the network…',
    empty:
      'No cast device answered. Switch your TV, speaker or player on and make sure it sits on the same network as OfflineU — in Docker the container needs network_mode: host to see it.',
    casting: 'Playing on {name}',
    playing: 'Playing',
    paused: 'Paused',
    finished: 'Finished',
    unknownDuration: 'length unknown',
    onLesson: '“{title}”',
    estimated: 'estimated from the clock',
    noDuration: 'length unknown - it will not be marked finished on its own',
    convertedHint: 'converted for this device',
    next: '⏭ Next',
    seekHint: 'Click the bar to jump to that position',
    openLesson: '📖 Open this lesson',
    nextUp: 'Next: {title}',
    onceHint: 'Stops when the lesson ends',
    loopHint: 'Repeats this lesson until you stop the cast',
    compatMode: 'Compatibility mode',
    compatHint:
      'Repackages the file while it plays (MPEG-TS) — needed for .mkv, .avi and friends. Exotic codecs are re-encoded; switch it off to hand over the untouched file.',
    noFfmpeg:
      'This file may be refused by your device. Install ffmpeg (or point OFFLINEU_FFMPEG at it) and OfflineU converts it while casting.',
    resumeFrom: 'Starts at {time} (the device takes over from there).',
    stop: '⏹ Stop',
    pause: '⏸ Pause',
    resume: '▶ Play'
  },
  toast: {
    loaded: 'Loaded "{name}"',
    enterPath: 'Please enter the path of a course folder.',
    removedFromRecent: 'Removed from the list, progress cleared',
    pickAnother: 'Pick another course',
    progressRefreshed: 'Progress refreshed',
    autoplayBlocked: 'Autoplay was blocked by the browser — press play to continue',
    couldNotLoadFile: 'Could not load this file: {message}',
    markedCompleted: 'Lesson marked as completed',
    skipForward: 'Fast-forward 10 s',
    skipBack: 'Rewind 10 s',
    volume: 'Volume {percent}%',
    castStarted: 'Casting “{title}” to {device}',
    castConverted: 'Converting “{title}” for {device} while it plays',
    castStopped: 'Stopped playback on {device}'
  }
}

const zh = {
  app: {
    tagline: '自托管离线课程播放器与进度追踪'
  },
  lang: {
    switchTo: '切换到 {label}'
  },
  common: {
    loading: '加载中…',
    refresh: '⟳ 刷新',
    up: '↑ 上一级',
    tryAgain: '重试',
    checkAgain: '⟳ 重新检查',
    open: '打开',
    loadCourse: '加载课程',
    remove: '移除课程（同时清除学习进度）',
    cancel: '取消',
    backToDashboard: '返回首页',
    backToCourse: '← 返回课程',
    previous: '← 上一节',
    next: '下一节 →',
    goTo: '返回 {label}'
  },
  home: {
    loadingLibrary: '正在加载课程库…'
  },
  picker: {
    noticeTitle: '🔌 还没有映射课程文件夹',
    noticeText:
      'OfflineU 只会读取你挂载进来的目录。请把课程文件夹映射到 /courses（建议只读）、可写的数据目录映射到 /app/data —— 镜像已声明这两个目录，飞牛OS 等 NAS 的 Docker 应用在创建容器时就会列出它们。无需重新构建或上传任何文件。',
    noticeFooterBefore: '已经映射好了？请确认里面有视频、音频或文档，并且冒号后面的路径与 ',
    noticeFooterAfter: ' 保持一致。',
    noticeTitleEmpty: '📂 {root} 已映射，但里面还没有文件',
    noticeTextEmpty:
      '文件夹存在而且可以读取，只是里面还没有任何文件（子文件夹里也没有）。把课程复制进去后再点『⟳ 重新检查』即可 —— Docker 里是映射的宿主文件夹，本地运行时就是 OFFLINEU_ROOTS 指向的文件夹。不需要重建或重启。',
    noticeTitleVolume: '🔌 {root} 上挂的是 Docker 临时卷，而不是你的文件夹',
    noticeTextVolume:
      '没有任何宿主文件夹绑定到 {root}：Docker 自己挂了一个卷。你放在 NAS 上的课程在这里看不到，OfflineU 写进去的内容也会在容器重建时丢失。',
    noticeTitleNotMounted: '🔌 容器里的 {root} 什么都没有挂载',
    noticeTextNotMounted:
      '{root} 上既没有绑定挂载也没有卷，说明映射没有生效 —— 在容器里容器路径必须正好是 {root}；本地运行时则表示该文件夹是空的。',
    noticeTitleUnreadable: '🔒 {root} 已映射，但容器没有读取权限',
    noticeTextUnreadable:
      'OfflineU 以非特权用户 uid 10001 运行，所以只有 root（或别的 NAS 用户）能读的宿主文件夹，即使映射正确也读不到。',
    noticeTitleMissing: '📍 映射没有生效：容器里找不到 {root}',
    noticeTextMissing:
      '容器里已经没有 {root} 这个目录了，重建容器即可恢复这个挂载点。',
    mountStatusTitle: '📌 映射状态',
    mountStatusOk: '已映射，{count} 项',
    mountStatusEmpty: '已映射，但里面是空的',
    mountStatusNotMounted: '这里没有挂载任何内容',
    mountStatusVolume: '现在是 Docker 卷，不是你的文件夹',
    mountStatusUnreadable: '已映射，但容器读不到',
    mountStatusMissing: '容器里没有这个目录',
    mappingFixFolders:
      '在 NAS 的 Docker 应用里打开容器设置（容器 → 设置 → 文件夹），核对两列：左边是宿主文件夹，右边必须分别是 /courses 和 /app/data。',
    mappingFixRecreate: '保存并让应用重建容器 —— 运行中的容器不会自动加载新的映射。',
    emptyFixCopy: '把你的课程文件复制到映射的宿主文件夹里。',
    emptyFixRecheck: '回到本页点『⟳ 重新检查』——不需要重启。',
    emptyFixBrowse: '也可以直接用下面的浏览器：只要文件夹里有视频、音频或文档，就能立刻加载。',
    unreadableFixChmod: '在 NAS 上让该目录可读：chmod -R a+rX “<宿主文件夹>”（飞牛OS 也可在文件夹权限里给“所有人”读权限）。',
    unreadableFixOwner:
      '更推荐：让容器以该文件夹的属主身份运行。飞牛OS 第一个用户通常是 1000:1000（用 id 命令查看），容器对话框里若有“用户/User（运行身份）”栏位就填它，compose 里写 user: "1000:1000"。',
    unreadableFixRoot:
      '实在改不了权限时才用 root：重建容器时加 --user 0:0（对话框里把用户填 root）。容器自己无法提权 —— 运行用户只能在创建容器时决定，创建后 docker update 也改不了。',
    unreadableFixData:
      '改成别的 uid 运行时，/app/data 也要让该 uid 可写（飞牛OS 上 /vol1/1000/offlineu-data 属于 1000）。',
    unreadableFixNoCaps:
      '本镜像内置读取能力（CAP_DAC_OVERRIDE），uid 10001 也能读任意映射目录 —— 但这只容器把它关掉了（no-new-privileges、--cap-drop DAC_OVERRIDE 或 --cap-drop ALL）。重建容器时去掉该选项，或按上面的办法处理。',
    recentTitle: '🕘 最近的课程',
    recentHint: '打开过的文件夹会被记住，一键即可切换回来。',
    recentProgressTitle: '已完成 {completed} / {total} 节课',
    removeConfirmTitle: '移除这门课程？',
    removeConfirmText: '“{name}” 将从最近列表中移除，其学习进度也会被删除，重新打开该文件夹将从零开始。此操作不可恢复。',
    removeConfirmAction: '移除并清除进度',
    selectTitle: '📂 选择课程',
    selectHint: '浏览到包含课程的文件夹并加载它。视频、音频、文档和测验会被自动识别。',
    manualPlaceholder: '…或直接粘贴文件夹路径，例如 D:\\Courses\\Python Tutorial',
    howToTitle: '🚀 使用说明',
    howTo: {
      prepareLabel: '整理文件',
      prepareText: '按文件夹结构存放课程内容（每个章节一个文件夹效果最好）。',
      browseLabel: '浏览',
      browseText: '找到课程文件夹，或在上方粘贴它的路径。',
      loadLabel: '加载',
      loadText: '然后开始学习 —— 进度会自动保存。',
      comeBackLabel: '随时回来',
      comeBackText: '：课程会被记住，重启后自动打开上次的课程。',
      autoplayLabel: '播放模式',
      autoplayText: '：播完后可以停止、循环本节或自动连播下一节，倍速设置也会保留。'
    },
    typesTitle: '🗂️ 支持的文件类型',
    types: {
      videoLabel: '视频',
      videoText: '— .mp4、.mkv、.avi、.mov、.webm、.m4v、.flv、.wmv',
      audioLabel: '音频',
      audioText: '— .mp3、.wav、.m4a、.aac、.ogg、.flac',
      docsLabel: '文档',
      docsText: '— .txt、.md、.html、.pdf、.docx、.doc、.rtf',
      subsLabel: '字幕',
      subsText: '— .srt、.vtt、.ass、.sub、.sbv（会即时转换为 WebVTT）',
      quizLabel: '测验',
      quizText: '— 文件名中包含 “quiz”、“exam” 或 “test” 的文档'
    },
    shortcuts: '课程页快捷键：空格 播放/暂停，← → 快退/快进 10 秒，↑ ↓ 调节音量。'
  },
  browser: {
    listing: '正在读取文件夹…',
    emptyNeedsMount: '这个文件夹里还没有挂载任何内容 —— 请查看上方的提示。',
    emptyMapped: '这个目录已映射，但里面还没有内容 —— 把课程文件复制到宿主文件夹即可。',
    empty: '这里没有子文件夹。可以把当前文件夹作为课程，或返回上一级。',
    mediaCount: '{count} 个媒体文件',
    courseBadge: '课程',
    useAsCourse: '作为课程',
    useCurrent: '把当前文件夹作为课程',
    actionsHint: '包含视频或音频的文件夹会被高亮，可以直接加载。'
  },
  dash: {
    changeCourse: '⇄ 更换课程',
    progressTitle: '📈 学习进度',
    chipLessons: '节课',
    chipCompleted: '已完成',
    chipRemaining: '剩余',
    resumeLabel: '从上次的位置继续',
    resumeButton: '▶ 继续学习',
    emptyLabel: '还没有开始学习',
    emptyTitle: '从下面的目录中选择一节开始',
    emptyText: '你的学习位置会自动记录，重启后依然保留。',
    contentTitle: '🧭 课程内容',
    searchPlaceholder: '搜索课程…',
    emptyContent: '这门课程暂时没有支持的文件。'
  },
  tree: {
    items: '{count} 项',
    completed: '已完成',
    notCompleted: '尚未完成'
  },
  progress: {
    lessonsCompleted: '节课已完成'
  },
  types: {
    video: '视频',
    audio: '音频',
    quiz: '测验',
    text: '文档'
  },
  lesson: {
    loading: '正在加载课程…',
    markCompleted: '标记为已完成',
    completed: '已完成 ✓',
    watched: '已观看',
    resumeAt: '上次看到',
    progressWarning: '进度未能保存。',
    speed: '播放速度',
    whenFinished: '播放结束时：',
    modeLoop: '单播循环',
    modeOnce: '单播不循环',
    modeNext: '连播',
    modeLoopHint: '播放结束后从头循环本节',
    modeOnceHint: '播放结束后停止',
    modeNextHint: '播放结束后自动播放下一节',
    loopHint: '本节将循环播放，直到切换模式',
    upNext: '接下来：',
    lastLesson: '这已经是课程的最后一节',
    subtitles: '字幕',
    subtitleSent: '字幕将随投屏一起发送到电视',
    contentTitle: '📄 相关文件',
    loadingResource: '加载中…',
    cannotPreview: '这种文件无法在浏览器中预览。请使用下面的链接，用对应的桌面应用打开它。',
    openInNewTab: '📎 在新标签页打开 {name}',
    shortcuts: '快捷键：空格 播放/暂停 · ← → 快退/快进 10 秒 · ↑ ↓ 调节音量'
  },
  cast: {
    button: '📺 投屏',
    title: '投屏到设备',
    refresh: '⟳ 重新搜索',
    scanning: '正在搜索局域网设备…',
    empty:
      '没有设备响应。请打开电视、音箱或播放器，并确认它与 OfflineU 在同一个网络里 —— Docker 部署时容器需要 network_mode: host 才能发现设备。',
    casting: '正在 {name} 上播放',
    playing: '播放中',
    paused: '已暂停',
    finished: '已播完',
    unknownDuration: '时长未知',
    onLesson: '“{title}”',
    estimated: '按时间估算',
    noDuration: '时长未知，不会自动标记完成',
    convertedHint: '已为设备转换',
    next: '⏭ 下一节',
    seekHint: '点击进度条可跳转到该位置',
    openLesson: '📖 打开这一节',
    nextUp: '下一节：{title}',
    onceHint: '结束后停止',
    loopHint: '结束后循环本节',
    compatMode: '兼容模式',
    compatHint:
      '播放时实时重新封装为 MPEG-TS —— .mkv、.avi 等格式需要它；编码不被支持时会自动转码。取消勾选则直接推送原始文件。',
    noFfmpeg:
      '设备可能会拒绝这个文件。安装 ffmpeg（或用 OFFLINEU_FFMPEG 指定路径）后，OfflineU 会在投屏时自动转换。',
    resumeFrom: '从 {time} 开始（设备会接着这个位置播放）。',
    stop: '⏹ 停止',
    pause: '⏸ 暂停',
    resume: '▶ 继续'
  },
  toast: {
    loaded: '已加载 “{name}”',
    enterPath: '请输入课程文件夹的路径。',
    removedFromRecent: '已从列表中移除，学习进度已清除',
    pickAnother: '请选择另一门课程',
    progressRefreshed: '进度已刷新',
    autoplayBlocked: '浏览器阻止了自动播放 —— 请点击播放按钮继续',
    couldNotLoadFile: '无法加载该文件：{message}',
    markedCompleted: '已标记为完成',
    skipForward: '快进 10 秒',
    skipBack: '后退 10 秒',
    volume: '音量 {percent}%',
    castStarted: '正在把 “{title}” 投屏到 {device}',
    castConverted: '正在边转码边把 “{title}” 播放到 {device}',
    castStopped: '已在 {device} 上停止播放'
  }
}

const messages = { en, zh }

// Falls back to the browser language: a Chinese browser opens in Chinese right
// away, everybody else starts in English.
function detectLocale() {
  try {
    const saved = window.localStorage.getItem(LOCALE_KEY)
    if (saved && messages[saved]) return saved
  } catch {
    /* storage disabled (private mode): keep detecting from the browser */
  }
  return String(navigator.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export const locale = ref(detectLocale())

function applyDocumentLanguage() {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = locale.value === 'zh' ? 'zh-CN' : 'en'
  }
}

export function setLocale(code) {
  if (!messages[code]) return
  locale.value = code
  try {
    window.localStorage.setItem(LOCALE_KEY, code)
  } catch {
    /* ignore: the choice then only lasts for this page */
  }
  applyDocumentLanguage()
}

export function toggleLocale() {
  setLocale(locale.value === 'zh' ? 'en' : 'zh')
}

function lookup(dict, key) {
  return key
    .split('.')
    .reduce((value, part) => (value && typeof value === 'object' ? value[part] : undefined), dict)
}

// Translates a key such as "toast.loaded"; {placeholders} are filled from vars.
// Unknown keys fall back to English and finally to the key itself.
export function t(key, vars) {
  const text = lookup(messages[locale.value], key) ?? lookup(messages.en, key) ?? key
  if (typeof text !== 'string') return key
  if (!vars) return text
  return text.replace(/\{(\w+)\}/g, (match, name) => (name in vars ? String(vars[name]) : match))
}

// The Go backend answers in English. While the UI is Chinese these rules make
// its error strings (and the "progress could not be saved" warning) readable;
// anything else is passed through untouched.
const SERVER_MESSAGES_ZH = [
  [/^Request failed with status (\d+)$/, '请求失败（HTTP $1）'],
  [
    /^cannot write progress to ([\s\S]+?): ([\s\S]+)\. Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable\.$/,
    '无法写入进度到 $1：$2. 请设置 OFFLINEU_PROGRESS_DIR 指向可写目录。'
  ],
  [/^not a directory: ([\s\S]+)$/, '不是文件夹：$1'],
  [/^access denied to ([\s\S]+?): ([\s\S]+)$/, '无法访问 $1：$2'],
  [/^directory not found: ([\s\S]+)$/, '找不到文件夹：$1'],
  [/^this path is outside the configured OFFLINEU_ROOTS$/, '该路径不在 OFFLINEU_ROOTS 允许的范围内'],
  [/^invalid course path$/, '课程路径无效'],
  [/^no course loaded$/, '尚未加载任何课程'],
  [/^lesson not found$/, '找不到该课时'],
  [/^lesson_path is required$/, '缺少 lesson_path 参数'],
  [/^path is required$/, '缺少 path 参数'],
  [/^cannot determine the home directory$/, '无法确定用户主目录'],
  [/^method not allowed$/, '该请求方法不被允许'],
  [/^dlna is disabled$/, '投屏功能已关闭'],
  [/^this lesson has no media to cast$/, '这一节没有可投屏的视频或音频'],
  [/^this lesson has no media to stream$/, '这一节没有可播放的视频或音频'],
  [/^ffmpeg is not installed, so this (lesson|file) cannot be converted$/, '未安装 ffmpeg，$1 无法转换'],
  [/^the cast device was not found on the network$/, '局域网里找不到这个投屏设备'],
  [/^action must be play, pause or stop$/, '操作只能是 play、pause 或 stop'],
  [/^device and lesson_path are required$/, '缺少 device 或 lesson_path 参数'],
  [/^device and action are required$/, '缺少 device 或 action 参数'],
  [/^([\s\S]+) did not accept the media: ([\s\S]+)$/, '$1 不接受这个媒体文件：$2'],
  [/^([\s\S]+) did not start playing: ([\s\S]+)$/, '$1 没有开始播放：$2'],
  [/^([\s\S]+) did not answer ([\s\S]+): ([\s\S]+)$/, '$1 没有响应 $2：$3']
]

export function translateServerMessage(message) {
  const text = String(message ?? '')
  if (locale.value !== 'zh') return text
  for (const [pattern, replacement] of SERVER_MESSAGES_ZH) {
    if (pattern.test(text)) return text.replace(pattern, replacement)
  }
  return text
}

// BCP-47 value sent to the backend so it can localise what it returns.
export function acceptLanguage() {
  return locale.value === 'zh' ? 'zh-CN' : 'en'
}

applyDocumentLanguage()
