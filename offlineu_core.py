#!/usr/bin/env python3
"""
OfflineU - Self-hosted Course Viewer & Tracker

Turns any local folder containing videos, audio, documents or quizzes into a
browsable dashboard with resumable playback and automatic progress tracking.
Everything stays on your machine: no cloud, no accounts, no telemetry.
"""

import argparse
import hashlib
import json
import mimetypes
import os
import platform
import re
import secrets
import string
import sys
import threading
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Optional
from urllib.parse import quote, unquote

from flask import (
    Flask,
    Response,
    jsonify,
    redirect,
    render_template,
    request,
    send_file,
    url_for,
)

BASE_DIR = Path(__file__).resolve().parent
TEMPLATES_DIR = BASE_DIR / 'templates'

app = Flask(__name__, template_folder=str(TEMPLATES_DIR))
app.config['SECRET_KEY'] = os.environ.get('OFFLINEU_SECRET_KEY') or secrets.token_hex(32)

# --------------------------------------------------------------------------- #
# Supported file types & tuning
# --------------------------------------------------------------------------- #

VIDEO_EXTENSIONS = {'.mp4', '.mkv', '.avi', '.mov', '.webm', '.m4v', '.flv', '.wmv'}
AUDIO_EXTENSIONS = {'.mp3', '.wav', '.m4a', '.aac', '.ogg', '.flac'}
SUBTITLE_EXTENSIONS = {'.srt', '.vtt', '.ass', '.sub', '.sbv'}
TEXT_EXTENSIONS = {'.txt', '.md', '.html', '.htm', '.pdf', '.docx', '.doc', '.rtf'}
PLAIN_TEXT_EXTENSIONS = {'.txt', '.md'}
INLINE_FRAME_EXTENSIONS = {'.html', '.htm', '.pdf'}
QUIZ_INDICATORS = {'quiz', 'exam', 'test', 'assessment', 'exercise', 'assignment', 'homework'}
SKIP_EXTENSIONS = {'.log', '.tmp', '.bak', '.swp'}

MIME_OVERRIDES = {
    '.mp4': 'video/mp4',
    '.m4v': 'video/mp4',
    '.webm': 'video/webm',
    '.mkv': 'video/x-matroska',
    '.mov': 'video/quicktime',
    '.avi': 'video/x-msvideo',
    '.flv': 'video/x-flv',
    '.wmv': 'video/x-ms-wmv',
    '.mp3': 'audio/mpeg',
    '.m4a': 'audio/mp4',
    '.aac': 'audio/aac',
    '.wav': 'audio/wav',
    '.ogg': 'audio/ogg',
    '.flac': 'audio/flac',
}

MAX_SCAN_DEPTH = 10
MAX_MEDIA_SCAN_ENTRIES = 5000
MAX_RECENT_COURSES = 20
PROGRESS_FILENAME = '.offlineu_progress.json'
STATE_FILENAME = 'offlineu_state.json'
REQUIRED_TEMPLATES = ('course_dashboard.html', 'lesson_view.html')

# --------------------------------------------------------------------------- #
# Path / text helpers
# --------------------------------------------------------------------------- #

def _norm_rel(value: Any) -> str:
    """Normalise a path to the forward-slash, course-relative form used everywhere."""
    return str(value).replace('\\', '/').strip('/')


def _title_slug(title: str) -> str:
    """URL friendly representation of a lesson title."""
    slug = re.sub(r'[^0-9A-Za-z._-]+', '_', title or '').strip('._')
    return slug or 'lesson'


def _allowed_roots() -> List[Path]:
    """Optional allow-list of directories OfflineU may browse and serve.

    Configure with the OFFLINEU_ROOTS environment variable (os.pathsep separated).
    When it is empty every local path is reachable, which keeps the behaviour of a
    plain local install; Docker users normally pin it to the mounted course folder.
    """
    raw = os.environ.get('OFFLINEU_ROOTS', '')
    roots: List[Path] = []
    for chunk in raw.split(os.pathsep):
        chunk = chunk.strip().strip('"')
        if not chunk:
            continue
        candidate = Path(chunk).expanduser()
        try:
            if candidate.is_dir():
                roots.append(candidate.resolve())
        except OSError:
            continue
    return roots


def _is_within(path: Path, root: Path) -> bool:
    """True when *path* is *root* or lives below it (symlinks resolved)."""
    try:
        candidate = os.path.normcase(str(Path(path).resolve()))
        base = os.path.normcase(str(Path(root).resolve()))
        return os.path.commonpath([candidate, base]) == base
    except (OSError, ValueError):  # ValueError: different drives on Windows
        return False


def _resolve_inside(base: Path, relative: str) -> Optional[Path]:
    """Resolve *relative* below *base*, or None when it would escape the directory."""
    if not relative or '\x00' in relative:
        return None
    try:
        candidate = (Path(base) / unquote(relative)).resolve()
    except (OSError, RuntimeError):
        return None
    if not _is_within(candidate, Path(base)):
        return None
    return candidate


def _progress_file_for(course_path: Path) -> Path:
    """Progress lives next to the course by default.

    OFFLINEU_PROGRESS_DIR relocates it into a central folder, which is what the
    Docker image uses so that course mounts can stay read-only.
    """
    central = os.environ.get('OFFLINEU_PROGRESS_DIR', '').strip()
    if not central:
        return Path(course_path) / PROGRESS_FILENAME
    key = hashlib.sha1(os.path.normcase(str(course_path)).encode('utf-8')).hexdigest()[:12]
    return Path(central).expanduser() / f'{key}_progress.json'


def _state_dir() -> Path:
    """Where OfflineU keeps its own bookkeeping (the courses you opened).

    Shares OFFLINEU_PROGRESS_DIR with the progress files when it is set (that is the
    writable volume in Docker); otherwise it uses ./data next to the app, which is
    already ignored by .gitignore and .dockerignore.
    """
    progress_dir = os.environ.get('OFFLINEU_PROGRESS_DIR', '').strip()
    if progress_dir:
        return Path(progress_dir).expanduser()
    return BASE_DIR / 'data'


def _state_file() -> Path:
    return _state_dir() / STATE_FILENAME


def _guess_mime(path: Any, fallback: str) -> str:
    """Extension first MIME lookup so the result does not depend on the OS registry."""
    override = MIME_OVERRIDES.get(Path(str(path)).suffix.lower())
    if override:
        return override
    guessed, _ = mimetypes.guess_type(str(path))
    return guessed or fallback


def _count_media_files(root: Path, limit: int = MAX_MEDIA_SCAN_ENTRIES) -> int:
    """Count video/audio files below *root*, giving up early on huge trees."""
    media = 0
    seen = 0
    stack = [Path(root)]
    while stack:
        directory = stack.pop()
        try:
            entries = list(os.scandir(directory))
        except OSError:
            continue
        for entry in entries:
            seen += 1
            if seen > limit:
                return media
            if entry.name.startswith('.'):
                continue
            try:
                if entry.is_dir(follow_symlinks=False):
                    stack.append(Path(entry.path))
                elif Path(entry.name).suffix.lower() in VIDEO_EXTENSIONS | AUDIO_EXTENSIONS:
                    media += 1
            except OSError:
                continue
    return media


_TIMESTAMP_RE = re.compile(r'(\d{1,3}:\d{2}:\d{2}),(\d{3})')
_INDEX_LINE_RE = re.compile(r'^\s*\d+\s*$')


def convert_srt_to_vtt(text: str) -> str:
    """Convert SubRip subtitles to WebVTT (browsers only understand VTT)."""
    text = text.lstrip('\ufeff').replace('\r\n', '\n').replace('\r', '\n')
    lines = []
    for line in text.split('\n'):
        if _INDEX_LINE_RE.match(line):
            continue
        lines.append(_TIMESTAMP_RE.sub(r'\1.\2', line))
    body = '\n'.join(lines).strip('\n')
    return f'WEBVTT\n\n{body}\n' if body else 'WEBVTT\n'


def _read_subtitle_text(path: Path) -> str:
    for encoding in ('utf-8-sig', 'utf-8', 'cp1252', 'latin-1'):
        try:
            return path.read_text(encoding=encoding)
        except UnicodeDecodeError:
            continue
    return path.read_text(encoding='utf-8', errors='replace')


# --------------------------------------------------------------------------- #
# Data model
# --------------------------------------------------------------------------- #

@dataclass
class Lesson:
    """One physical file of the course (one file == one lesson)."""

    title: str
    path: str                      # absolute path on disk
    rel_path: str                  # course relative path (stable progress key)
    url: str                       # value used in /lesson/<url>
    lesson_type: str               # 'video', 'audio', 'text' or 'quiz'
    video_file: Optional[str] = None
    video_mime: Optional[str] = None
    audio_file: Optional[str] = None
    audio_mime: Optional[str] = None
    subtitle_file: Optional[str] = None
    text_files: List[str] = field(default_factory=list)
    completed: bool = False
    last_accessed: Optional[str] = None
    progress_seconds: int = 0

    @property
    def video_src(self) -> Optional[str]:
        return f'/files/{quote(self.video_file)}' if self.video_file else None

    @property
    def audio_src(self) -> Optional[str]:
        return f'/files/{quote(self.audio_file)}' if self.audio_file else None

    @property
    def subtitle_src(self) -> Optional[str]:
        return f'/subtitles/{quote(self.subtitle_file)}' if self.subtitle_file else None


@dataclass
class DirectoryNode:
    """A directory of the course tree."""

    name: str
    path: str
    type: str = 'directory'
    children: Dict[str, 'DirectoryNode'] = field(default_factory=dict)
    lessons: List[Lesson] = field(default_factory=list)
    has_content: bool = False


@dataclass
class Course:
    name: str
    path: str
    root_node: DirectoryNode
    progress_file: str
    last_accessed_path: Optional[str] = None
    last_accessed_url: Optional[str] = None
    last_accessed_title: Optional[str] = None


# --------------------------------------------------------------------------- #
# Course parsing
# --------------------------------------------------------------------------- #

class DynamicCourseParser:
    """Builds the directory tree of a course and the lessons inside it."""

    @staticmethod
    def scan_directory(course_path: Any) -> Course:
        base = Path(str(course_path)).expanduser()
        try:
            base = base.resolve()
        except OSError as exc:
            raise ValueError(f'Invalid course path: {course_path} ({exc})') from exc
        if not base.is_dir():
            raise ValueError(f'Invalid course path: {course_path}')

        print(f'Scanning course: {base.name}')
        root_node = DynamicCourseParser._build_directory_tree(base, base)
        return Course(
            name=base.name,
            path=str(base),
            root_node=root_node,
            progress_file=str(_progress_file_for(base)),
        )

    @staticmethod
    def _build_directory_tree(course_path: Path, current_path: Path, depth: int = 0) -> DirectoryNode:
        """Recursively map a directory (hidden files/folders are skipped)."""
        node_name = 'Course Root' if current_path == course_path else current_path.name
        node = DirectoryNode(name=node_name, path=str(current_path))
        if depth > MAX_SCAN_DEPTH:
            return node

        try:
            entries = sorted(current_path.iterdir(), key=lambda item: (item.is_file(), item.name.lower()))
        except (PermissionError, OSError) as exc:
            print(f'Warning: cannot read {current_path}: {exc}')
            return node

        subtitles: List[Path] = []
        for item in entries:
            if item.name.startswith('.'):
                continue
            try:
                is_directory = item.is_dir()
            except OSError:
                continue

            if is_directory:
                child = DynamicCourseParser._build_directory_tree(course_path, item, depth + 1)
                if child.has_content or child.children or child.lessons:
                    node.children[child.name] = child
                    node.has_content = True
            elif item.suffix.lower() in SUBTITLE_EXTENSIONS:
                subtitles.append(item)
            else:
                lesson = DynamicCourseParser._create_lesson_from_file(item, course_path)
                if lesson is not None:
                    node.lessons.append(lesson)
                    node.has_content = True

        DynamicCourseParser._attach_subtitles(node.lessons, subtitles, course_path)
        return node

    @staticmethod
    def _attach_subtitles(lessons: List[Lesson], subtitles: List[Path], course_path: Path) -> None:
        """Hand subtitle files over to the media file they belong to."""
        media_lessons = [lesson for lesson in lessons if lesson.video_file or lesson.audio_file]
        if not media_lessons:
            return
        for subtitle in subtitles:
            target = next(
                (lesson for lesson in media_lessons
                 if Path(lesson.path).stem.lower() == subtitle.stem.lower()),
                None,
            )
            if target is None and len(media_lessons) == 1:
                target = media_lessons[0]
            if target is None or target.subtitle_file:
                continue
            target.subtitle_file = _norm_rel(subtitle.relative_to(course_path))


    @staticmethod
    def _create_lesson_from_file(file_path: Path, course_path: Path) -> Optional[Lesson]:
        """Create a lesson from a single file, or None when it is not course content."""
        extension = file_path.suffix.lower()
        if extension in SKIP_EXTENSIONS:
            return None

        relative = _norm_rel(file_path.relative_to(course_path))
        title = DynamicCourseParser._clean_lesson_name(file_path.stem)
        lesson = Lesson(
            title=title,
            path=str(file_path),
            rel_path=relative,
            url=f'{relative}/{_title_slug(title)}',
            lesson_type='text',
        )

        if extension in VIDEO_EXTENSIONS:
            lesson.lesson_type = 'video'
            lesson.video_file = relative
            lesson.video_mime = _guess_mime(file_path, 'video/mp4')
        elif extension in AUDIO_EXTENSIONS:
            lesson.lesson_type = 'audio'
            lesson.audio_file = relative
            lesson.audio_mime = _guess_mime(file_path, 'audio/mpeg')
        elif extension in TEXT_EXTENSIONS:
            lesson.text_files.append(relative)
            if any(indicator in file_path.name.lower() for indicator in QUIZ_INDICATORS):
                lesson.lesson_type = 'quiz'
        else:
            return None  # unsupported file type
        return lesson

    @staticmethod
    def _clean_lesson_name(name: str) -> str:
        """Human readable title: '01 - Intro' -> 'Intro'."""
        name = re.sub(r'^\d+[.\-_\s]*', '', name)
        name = re.sub(r'[-_]+', ' ', name)
        name = ' '.join(word.capitalize() for word in name.split() if word)
        return name if name.strip() else 'Untitled Lesson'

    @staticmethod
    def _calculate_completion_stats(node: DirectoryNode) -> Dict[str, Any]:
        """Count lessons and completed lessons of a (sub)tree."""
        total_lessons = 0
        completed_lessons = 0

        def count_lessons_recursive(current: DirectoryNode) -> None:
            nonlocal total_lessons, completed_lessons
            for lesson in current.lessons:
                total_lessons += 1
                if lesson.completed:
                    completed_lessons += 1
            for child in current.children.values():
                count_lessons_recursive(child)

        count_lessons_recursive(node)
        percentage = (completed_lessons / total_lessons * 100) if total_lessons else 0.0
        return {
            'total_lessons': total_lessons,
            'completed_lessons': completed_lessons,
            'completion_percentage': round(percentage, 1),
        }


# --------------------------------------------------------------------------- #
# Progress tracking
# --------------------------------------------------------------------------- #

_progress_lock = threading.RLock()


class ProgressStorageError(RuntimeError):
    """Raised when progress cannot be persisted (e.g. read-only course folder)."""


class ProgressTracker:
    """Reads and writes lesson progress. Every write is locked and atomic."""

    @staticmethod
    def load_progress(course: Course) -> Dict[str, Any]:
        with _progress_lock:
            try:
                with open(course.progress_file, 'r', encoding='utf-8') as handle:
                    data = json.load(handle)
            except FileNotFoundError:
                return {}
            except (json.JSONDecodeError, OSError) as exc:
                print(f'Warning: cannot read progress file {course.progress_file}: {exc}')
                return {}
        return data if isinstance(data, dict) else {}

    @staticmethod
    def save_progress(course: Course, progress_data: Dict[str, Any]) -> None:
        """Write the progress file atomically so a crash can never truncate it."""
        target = Path(course.progress_file)
        temp_file = target.with_name(target.name + '.tmp')
        with _progress_lock:
            try:
                target.parent.mkdir(parents=True, exist_ok=True)
                with open(temp_file, 'w', encoding='utf-8') as handle:
                    json.dump(progress_data, handle, indent=2, ensure_ascii=False)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.replace(temp_file, target)
            except OSError as exc:
                raise ProgressStorageError(
                    f'Cannot write progress to {target}: {exc}. '
                    'Use OFFLINEU_PROGRESS_DIR to store progress somewhere writable.'
                ) from exc
            finally:
                if temp_file.exists():
                    try:
                        temp_file.unlink()
                    except OSError:
                        pass

    @staticmethod
    def update_lesson_progress(course: Course, lesson_key: str, completed: Optional[bool] = None,
                               progress_seconds: Optional[int] = None) -> Dict[str, Any]:
        """Partially update a lesson entry.

        ``None`` means "keep what is stored", so simply opening a lesson no longer
        wipes its completion flag (regression test covers this). Watched seconds are
        never allowed to move backwards.
        """
        lesson_key = _norm_rel(lesson_key)
        with _progress_lock:
            progress = ProgressTracker.load_progress(course)
            entry = progress.get(lesson_key)
            if not isinstance(entry, dict):
                entry = {}
            if completed is not None:
                entry['completed'] = bool(completed)
            if progress_seconds is not None:
                entry['progress_seconds'] = max(int(progress_seconds), int(entry.get('progress_seconds') or 0))
            entry['last_accessed'] = datetime.now().isoformat(timespec='seconds')
            progress[lesson_key] = entry
            progress['last_accessed_path'] = lesson_key
            ProgressTracker.save_progress(course, progress)
        return entry

    @staticmethod
    def record_access(course: Course, lesson_key: str) -> Dict[str, Any]:
        """Remember that a lesson was opened without touching its progress values."""
        return ProgressTracker.update_lesson_progress(course, lesson_key)


    @staticmethod
    def apply_progress_to_tree(course: Course) -> None:
        """Push the stored values back onto the parsed tree."""
        progress = ProgressTracker.load_progress(course)

        def lookup(lesson: Lesson) -> Optional[Dict[str, Any]]:
            # rel_path/url are the current keys, the third one is the legacy format
            for key in (lesson.rel_path, lesson.url,
                        f"{lesson.rel_path}/{lesson.title.replace(' ', '_')}"):
                entry = progress.get(key)
                if isinstance(entry, dict):
                    return entry
            return None

        def apply_to_node(node: DirectoryNode) -> None:
            for lesson in node.lessons:
                entry = lookup(lesson)
                if entry:
                    lesson.completed = bool(entry.get('completed', False))
                    lesson.last_accessed = entry.get('last_accessed')
                    lesson.progress_seconds = int(entry.get('progress_seconds') or 0)
            for child in node.children.values():
                apply_to_node(child)

        apply_to_node(course.root_node)

        course.last_accessed_path = progress.get('last_accessed_path')
        course.last_accessed_url = None
        course.last_accessed_title = None
        if course.last_accessed_path:
            lesson = find_lesson_in_tree(course.root_node, course.last_accessed_path)
            if lesson is not None:
                course.last_accessed_url = lesson.url
                course.last_accessed_title = lesson.title

    @staticmethod
    def get_completion_stats(course: Course) -> Dict[str, Any]:
        """Completion counters plus everything the "continue" card needs."""
        stats = DynamicCourseParser._calculate_completion_stats(course.root_node)
        stats['last_accessed_path'] = course.last_accessed_path
        stats['last_accessed_url'] = course.last_accessed_url
        stats['last_accessed_title'] = course.last_accessed_title
        return stats


class CourseStore:
    """Thread-safe holder for the active course plus the list of courses you opened.

    OfflineU is a single-user/self-hosted tool, but Flask serves requests from a
    thread pool, so the active course is guarded instead of being a bare global.

    The selection is *also* written to disk (``active_course`` + ``recent_courses``),
    so going back to the home page, restarting the server or restarting the container
    no longer loses the course you added.
    """

    def __init__(self, state_file: Optional[Path] = None) -> None:
        self._lock = threading.RLock()
        self._course: Optional[Course] = None
        self._override = Path(state_file) if state_file else None

    # ------------------------------------------------------------------ state --
    @property
    def state_file(self) -> Path:
        """Resolved lazily so tests and OFFLINEU_PROGRESS_DIR changes are honoured."""
        return self._override or _state_file()

    def _read_state(self) -> Dict[str, Any]:
        try:
            with open(self.state_file, 'r', encoding='utf-8') as handle:
                data = json.load(handle)
        except FileNotFoundError:
            return {}
        except (OSError, json.JSONDecodeError) as exc:
            print(f'Warning: cannot read {self.state_file}: {exc}')
            return {}
        return data if isinstance(data, dict) else {}

    def _write_state(self, state: Dict[str, Any]) -> None:
        target = self.state_file
        temp_file = target.with_name(target.name + '.tmp')
        try:
            target.parent.mkdir(parents=True, exist_ok=True)
            with open(temp_file, 'w', encoding='utf-8') as handle:
                json.dump(state, handle, indent=2, ensure_ascii=False)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temp_file, target)
        except OSError as exc:
            # Remembering the selection is a convenience, never a hard failure.
            print(f'Warning: cannot remember courses in {target}: {exc}')
        finally:
            if temp_file.exists():
                try:
                    temp_file.unlink()
                except OSError:
                    pass

    @staticmethod
    def _remember(state: Dict[str, Any], course: Course) -> List[Dict[str, Any]]:
        entry = {
            'path': course.path,
            'name': course.name,
            'last_opened': datetime.now().isoformat(timespec='seconds'),
        }
        recent = [item for item in state.get('recent_courses', [])
                  if isinstance(item, dict) and item.get('path') != course.path]
        recent.insert(0, entry)
        return recent[:MAX_RECENT_COURSES]

    # ---------------------------------------------------------- active course --
    def get(self) -> Optional[Course]:
        with self._lock:
            return self._course

    def set(self, course: Optional[Course]) -> None:
        with self._lock:
            self._course = course
            state = self._read_state()
            if course is None:
                state.pop('active_course', None)
            else:
                state['active_course'] = course.path
                state['recent_courses'] = self._remember(state, course)
            self._write_state(state)

    def clear(self) -> None:
        """Stop showing the current course, but keep it in the recent list."""
        self.set(None)

    def recent_courses(self) -> List[Dict[str, Any]]:
        """Remembered courses that still exist on disk, most recent first.

        Entries whose folder disappeared (or that fall outside OFFLINEU_ROOTS) are
        skipped, so the picker never offers something that cannot be opened.
        """
        with self._lock:
            recent = self._read_state().get('recent_courses', [])
        roots = _allowed_roots()
        result = []
        for item in recent:
            if not isinstance(item, dict):
                continue
            path = Path(str(item.get('path', '')))
            if not path.is_dir():
                continue
            if roots and not any(_is_within(path, root) for root in roots):
                continue
            result.append({
                'path': str(path),
                'name': str(item.get('name') or path.name),
                'last_opened': item.get('last_opened'),
            })
        return result

    def forget(self, path: str) -> None:
        """Drop a single course from the recent list."""
        target = os.path.normcase(os.path.abspath(str(path)))
        with self._lock:
            state = self._read_state()
            state['recent_courses'] = [
                item for item in state.get('recent_courses', [])
                if not (isinstance(item, dict)
                        and os.path.normcase(os.path.abspath(str(item.get('path', '')))) == target)
            ]
            self._write_state(state)

    def restore(self) -> Optional[Course]:
        """Reload the remembered course (home page visit, restart, new worker).

        Returns None when there is nothing to restore, the folder disappeared or it is
        outside OFFLINEU_ROOTS. The entry stays in the recent list either way, so it can
        still be opened from the picker.
        """
        with self._lock:
            remembered = str(self._read_state().get('active_course') or '')
        if not remembered:
            return None

        path = Path(remembered).expanduser()
        roots = _allowed_roots()
        if not path.is_dir():
            print(f'Warning: not restoring {path}: the folder no longer exists')
            self.clear()
            return None
        if roots and not any(_is_within(path, root) for root in roots):
            print(f'Warning: not restoring {path}: it is outside OFFLINEU_ROOTS')
            self.clear()
            return None

        try:
            course = DynamicCourseParser.scan_directory(path)
        except (ValueError, OSError) as exc:
            print(f'Warning: cannot restore {path}: {exc}')
            self.clear()
            return None

        self.set(course)
        return course



course_store = CourseStore()

# --------------------------------------------------------------------------- #
# Routes
# --------------------------------------------------------------------------- #

EMPTY_STATS: Dict[str, Any] = {
    'total_lessons': 0,
    'completed_lessons': 0,
    'completion_percentage': 0.0,
    'last_accessed_path': None,
    'last_accessed_url': None,
    'last_accessed_title': None,
}


@app.route('/')
def index():
    """Course dashboard; doubles as the course picker when nothing is loaded."""
    course = course_store.get()
    if course is None:
        # Coming back to the home page - or hitting it after a restart / in a fresh
        # worker process - must not lose the course that was opened before.
        course = course_store.restore()

    if course is None:
        return render_template(
            'course_dashboard.html',
            course=None,
            stats=dict(EMPTY_STATS),
            browse_roots=[str(root) for root in _allowed_roots()],
            recent_courses=course_store.recent_courses(),
        )

    ProgressTracker.apply_progress_to_tree(course)
    return render_template(
        'course_dashboard.html',
        course=course,
        stats=ProgressTracker.get_completion_stats(course),
        browse_roots=[],
        recent_courses=[],
    )


def _windows_drives() -> List[Dict[str, Any]]:
    drives = []
    for letter in string.ascii_uppercase:
        drive = Path(f'{letter}:\\')
        try:
            if drive.exists():
                drives.append({'name': f'Drive {letter}:', 'path': str(drive),
                               'media_files': 0, 'is_course_candidate': False})
        except OSError:
            continue
    return drives


def _browse_payload(current: Path, roots: List[Path]):
    """Describe one directory level for the course picker."""
    try:
        entries = sorted(current.iterdir(), key=lambda item: item.name.lower())
    except (PermissionError, OSError) as exc:
        return jsonify({'error': f'Access denied to {current}: {exc}'}), 403

    directories = []
    for item in entries:
        if item.name.startswith('.'):
            continue
        try:
            if not item.is_dir():
                continue
            media_files = _count_media_files(item)
        except (PermissionError, OSError):
            directories.append({'name': f'{item.name} (access denied)', 'path': str(item),
                                'media_files': 0, 'is_course_candidate': False})
            continue
        directories.append({'name': item.name, 'path': str(item),
                            'media_files': media_files,
                            'is_course_candidate': media_files > 0})

    parent = None
    parent_path = current.parent
    if parent_path != current and (not roots or any(_is_within(parent_path, root) for root in roots)):
        parent = str(parent_path)

    return jsonify({
        'current_path': str(current),
        'parent_path': parent,
        'directories': directories,
        'roots': [str(root) for root in roots],
    })


@app.route('/browse')
def browse_directories():
    """JSON directory browser used by the course picker."""
    roots = _allowed_roots()
    requested = request.args.get('path', '').strip()

    try:
        if not requested:
            if roots:
                return _browse_payload(roots[0], roots)
            if platform.system() == 'Windows':
                return jsonify({'current_path': 'Select a Drive', 'parent_path': None,
                                'directories': _windows_drives(), 'roots': []})
            return _browse_payload(Path.home(), roots)

        current = Path(unquote(requested)).expanduser()
        if roots and not any(_is_within(current, root) for root in roots):
            return jsonify({'error': 'This path is outside the configured OFFLINEU_ROOTS'}), 403
        if not current.is_dir():
            return jsonify({'error': f'Not a directory: {current}'}), 404
        return _browse_payload(current, roots)
    except (PermissionError, OSError) as exc:
        return jsonify({'error': f'Access denied: {exc}'}), 403


@app.route('/load_course', methods=['POST'])
def load_course():
    """Parse a directory and make it the active course."""
    payload = request.get_json(silent=True) or {}
    course_path = str(payload.get('course_path') or '').strip()
    if not course_path:
        return jsonify({'error': 'Invalid course path'}), 400

    candidate = Path(course_path).expanduser()
    if not candidate.is_dir():
        return jsonify({'error': f'Directory not found: {course_path}'}), 400

    roots = _allowed_roots()
    if roots and not any(_is_within(candidate, root) for root in roots):
        return jsonify({'error': 'This path is outside the configured OFFLINEU_ROOTS'}), 403

    try:
        course = DynamicCourseParser.scan_directory(candidate)
    except (ValueError, OSError) as exc:
        return jsonify({'error': str(exc)}), 500

    course_store.set(course)
    return jsonify({'success': True, 'course_name': course.name})


def get_all_lessons(node: DirectoryNode) -> List[Lesson]:
    """Flatten the tree into the order lessons are shown in."""
    lessons: List[Lesson] = []

    def collect(current: DirectoryNode) -> None:
        lessons.extend(current.lessons)
        for child in current.children.values():
            collect(child)

    collect(node)
    return lessons


def find_lesson_in_tree(node: DirectoryNode, target_path: str) -> Optional[Lesson]:
    """Resolve a lesson from its url / relative path (legacy formats included)."""
    target = _norm_rel(unquote(target_path or ''))
    if not target:
        return None

    for lesson in node.lessons:
        if target in (lesson.url, lesson.rel_path, lesson.path,
                      f"{lesson.rel_path}/{lesson.title.replace(' ', '_')}"):
            return lesson
    for child in node.children.values():
        found = find_lesson_in_tree(child, target)
        if found is not None:
            return found
    return None


def build_text_resources(lesson: Lesson) -> List[Dict[str, str]]:
    """Describe how every document of a lesson has to be rendered."""
    resources = []
    for file_name in lesson.text_files:
        suffix = Path(file_name).suffix.lower()
        if suffix in INLINE_FRAME_EXTENSIONS:
            mode = 'iframe'      # html and pdf can be embedded by the browser
        elif suffix in PLAIN_TEXT_EXTENSIONS:
            mode = 'text'        # fetched and shown as plain text
        else:
            mode = 'download'    # doc/docx/rtf cannot be previewed
        resources.append({
            'name': Path(file_name).name,
            'url': file_name,
            'src': f'/files/{quote(file_name)}',
            'mode': mode,
        })
    return resources


@app.route('/lesson/<path:lesson_path>')
def view_lesson(lesson_path: str):
    """Render one lesson together with its neighbours."""
    course = course_store.get()
    if course is None:
        return redirect(url_for('index'))

    lesson = find_lesson_in_tree(course.root_node, lesson_path)
    if lesson is None:
        return redirect(url_for('index'))

    # Refresh the tree first so deep links (and reloads) show the stored completion
    # state and resume position instead of the values parsed a while ago.
    ProgressTracker.apply_progress_to_tree(course)

    lessons = get_all_lessons(course.root_node)
    position = next((index for index, item in enumerate(lessons) if item is lesson), -1)
    prev_lesson = lessons[position - 1].url if position > 0 else None
    next_lesson = lessons[position + 1].url if 0 <= position < len(lessons) - 1 else None

    # What plays after this lesson ends: the next playable lesson (documents are skipped
    # because they cannot be played), falling back to the next lesson so a course whose
    # tail is made of documents still advances instead of stalling.
    later_lessons = lessons[position + 1:] if position >= 0 else []
    autoplay_lesson = next((item for item in later_lessons if item.video_file or item.audio_file), None)
    if autoplay_lesson is None and later_lessons:
        autoplay_lesson = later_lessons[0]
    autoplay_href = f'/lesson/{quote(autoplay_lesson.url)}?autoplay=1' if autoplay_lesson else None

    storage_warning = None
    try:
        ProgressTracker.record_access(course, lesson.rel_path)
    except ProgressStorageError as exc:
        storage_warning = str(exc)
        print(f'Warning: {exc}')

    return render_template('lesson_view.html',
                           course=course,
                           lesson=lesson,
                           lesson_path=lesson.url,
                           text_resources=build_text_resources(lesson),
                           prev_lesson=prev_lesson,
                           next_lesson=next_lesson,
                           autoplay=request.args.get('autoplay') == '1',
                           autoplay_href=autoplay_href,
                           autoplay_title=autoplay_lesson.title if autoplay_lesson else None,
                           storage_warning=storage_warning)


@app.route('/api/progress', methods=['POST'])
def update_progress():
    """Store progress for one lesson; omitted fields are left untouched."""
    course = course_store.get()
    if course is None:
        return jsonify({'error': 'No course loaded'}), 400

    payload = request.get_json(silent=True) or {}
    raw_path = str(payload.get('lesson_path') or '').strip()
    if not raw_path:
        return jsonify({'error': 'lesson_path is required'}), 400

    lesson = find_lesson_in_tree(course.root_node, raw_path)
    lesson_key = lesson.rel_path if lesson is not None else _norm_rel(raw_path)

    completed = payload.get('completed')
    completed = None if completed is None else bool(completed)

    seconds = payload.get('progress_seconds')
    if isinstance(seconds, bool) or not isinstance(seconds, (int, float)):
        seconds = None

    try:
        entry = ProgressTracker.update_lesson_progress(course, lesson_key, completed, seconds)
    except ProgressStorageError as exc:
        return jsonify({'error': str(exc)}), 500
    return jsonify({'success': True, 'lesson_key': lesson_key, 'progress': entry})


def _course_file(course: Course, filepath: str):
    """Resolve a course file and reject anything outside the course/allowed roots."""
    full_path = _resolve_inside(Path(course.path), filepath)
    if full_path is None:
        return None, ('Access denied', 403)
    roots = _allowed_roots()
    if roots and not any(_is_within(full_path, root) for root in roots):
        return None, ('Access denied', 403)
    if not full_path.is_file():
        return None, ('File not found', 404)
    return full_path, None


@app.route('/files/<path:filepath>')
def serve_file(filepath: str):
    """Serve any file that lives inside the active course directory."""
    course = course_store.get()
    if course is None:
        return 'No course loaded', 404
    full_path, error = _course_file(course, filepath)
    if error:
        return error
    return send_file(str(full_path),
                     mimetype=_guess_mime(full_path, 'application/octet-stream'),
                     conditional=True)


@app.route('/subtitles/<path:filepath>')
def serve_subtitle(filepath: str):
    """Serve subtitles as WebVTT, converting SRT (and friends) on the fly."""
    course = course_store.get()
    if course is None:
        return 'No course loaded', 404
    full_path, error = _course_file(course, filepath)
    if error:
        return error

    if full_path.suffix.lower() == '.vtt':
        body = full_path.read_text(encoding='utf-8', errors='replace')
    else:
        body = convert_srt_to_vtt(_read_subtitle_text(full_path))
    return Response(body, mimetype='text/vtt')


@app.route('/health')
def healthcheck():
    """Healthcheck endpoint for Docker."""
    return jsonify({'status': 'healthy'}), 200


@app.route('/reset_course')
def reset_course():
    """Go back to the picker; the course stays in the recent list for one-click return."""
    course_store.clear()
    return redirect(url_for('index'))


@app.route('/forget_course')
def forget_course():
    """Remove a single entry from the recent course list."""
    path = request.args.get('path', '')
    if path:
        course_store.forget(path)
    return redirect(url_for('index'))


# --------------------------------------------------------------------------- #
# Templates & entry point
# --------------------------------------------------------------------------- #

def missing_templates() -> List[str]:
    return [name for name in REQUIRED_TEMPLATES if not (TEMPLATES_DIR / name).is_file()]


def ensure_templates() -> None:
    """Fail loudly when the HTML templates are missing.

    Older versions embedded a second, hand-maintained copy of the templates in
    this file and wrote them out with --create-templates. That copy drifted away
    from the real UI (it still used the removed ``course.modules`` model), so the
    templates in ./templates are now the single source of truth.
    """
    missing = missing_templates()
    if missing:
        raise RuntimeError(
            'Missing template files: ' + ', '.join(missing) + '. Restore the '
            f'{TEMPLATES_DIR} directory from the repository (git clone or checkout).'
        )


def create_templates() -> List[str]:
    """Backwards compatible helper for --create-templates: validates + reports."""
    if not TEMPLATES_DIR.exists():
        try:
            TEMPLATES_DIR.mkdir(parents=True)
        except OSError as exc:
            print(f'Error: cannot create {TEMPLATES_DIR}: {exc}', file=sys.stderr)
            return list(REQUIRED_TEMPLATES)
    return missing_templates()


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description='OfflineU Course Viewer & Tracker')
    parser.add_argument('--host', default=os.environ.get('OFFLINEU_HOST', '127.0.0.1'),
                        help='Host to bind to (default: 127.0.0.1, env OFFLINEU_HOST)')
    parser.add_argument('--port', type=int, default=int(os.environ.get('OFFLINEU_PORT', '5000')),
                        help='Port to bind to (default: 5000, env OFFLINEU_PORT)')
    parser.add_argument('--debug', action='store_true', help='Enable Flask debug mode')
    parser.add_argument('--create-templates', '--check-templates', dest='create_templates',
                        action='store_true',
                        help='Check that the HTML templates are present '
                             '(creates the templates/ directory if it is missing)')
    parser.add_argument('course_path', nargs='?',
                        help='Path to a course directory to load at startup')
    args = parser.parse_args(argv)

    if args.create_templates:
        missing = create_templates()
        if missing:
            print('Missing template files: ' + ', '.join(missing), file=sys.stderr)
            print(f'Restore them in {TEMPLATES_DIR} from the repository; OfflineU no longer '
                  'generates placeholder templates.', file=sys.stderr)
            return 1
        print('Templates OK: ' + ', '.join(REQUIRED_TEMPLATES))
        if not args.course_path:
            return 0

    try:
        ensure_templates()
    except RuntimeError as exc:
        print(f'Error: {exc}', file=sys.stderr)
        return 1

    roots = _allowed_roots()
    if roots:
        print('Browsing confined to OFFLINEU_ROOTS: ' + os.pathsep.join(str(root) for root in roots))
    if os.environ.get('OFFLINEU_PROGRESS_DIR'):
        print(f'Progress files go to {os.environ["OFFLINEU_PROGRESS_DIR"]}')

    course_path = args.course_path or os.environ.get('AUTO_LOAD_COURSE')
    if course_path:
        candidate = Path(course_path).expanduser()
        if not candidate.is_dir():
            print(f'Error: course path does not exist: {course_path}', file=sys.stderr)
            return 1
        if roots and not any(_is_within(candidate, root) for root in roots):
            print('Warning: the course is outside OFFLINEU_ROOTS; its files will not be served.',
                  file=sys.stderr)
        try:
            course = DynamicCourseParser.scan_directory(candidate)
        except (ValueError, OSError) as exc:
            print(f'Error loading course: {exc}', file=sys.stderr)
            if args.debug:
                import traceback
                traceback.print_exc()
            return 1
        course_store.set(course)
        print(f'Auto-loaded course: {course.name} '
              f'({len(course.root_node.children)} top-level folders)')
    else:
        # No course on the command line: bring back the one from last time, so a
        # restart does not look like "my course was deleted".
        restored = course_store.restore()
        if restored is not None:
            print(f'Restored last course: {restored.name} ({restored.path})')
        remembered = course_store.recent_courses()
        if remembered:
            print(f'{len(remembered)} remembered course(s) available in the picker')

    print(f'Course bookkeeping file: {course_store.state_file}')

    print(f'Starting OfflineU on http://{args.host}:{args.port}')
    print('Use --create-templates to verify the template directory')

    try:
        app.run(debug=args.debug, host=args.host, port=args.port)
    except KeyboardInterrupt:
        print('\nShutting down OfflineU...')
    except OSError as exc:
        print(f'Error starting server: {exc}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())










