"""Regression tests for OfflineU.

Standard library only (unittest + Flask's test client), so the suite runs offline:

    python -m unittest discover -s tests -v
"""

import json
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import offlineu_core as core  # noqa: E402

SRT_SAMPLE = """1
00:00:01,000 --> 00:00:04,000
Hello OfflineU

2
00:00:05,500 --> 00:00:07,000
Second cue
"""


class OfflineUTestCase(unittest.TestCase):
    """Builds a temporary course, an isolated progress folder and a test client."""

    def setUp(self):
        self.tmp_dir = Path(tempfile.mkdtemp(prefix='offlineu_test_'))
        self.progress_dir = self.tmp_dir / 'progress'
        self.course_dir = self.tmp_dir / 'Python Tutorial'
        (self.course_dir / 'Section 1').mkdir(parents=True)
        (self.course_dir / 'Section 2' / 'resources').mkdir(parents=True)

        self.write('Section 1/01 - Intro.mp4', b'\x00' * 64)
        self.write('Section 1/01 - Intro.srt', SRT_SAMPLE)
        self.write('Section 1/02 - Notes.txt', 'Hello OfflineU')
        self.write('Section 1/03 - Quiz.html', '<html><body><h1>Q1</h1></body></html>')
        self.write('Section 1/04 - Slides.pdf', b'%PDF-1.4 fake')
        self.write('Section 1/05 - Handout.docx', b'PK\x03\x04 fake')
        # a second video, placed *after* the documents, to test autoplay skipping them
        self.write('Section 1/06 - Wrap Up.mp4', b'\x00' * 64)
        self.write('Section 2/resources/extras.md', '# Extras')
        self.write('Section 2/.hidden.md', 'should not show up')
        self.write('ignore.zip', b'zip')
        (self.tmp_dir / 'secret.txt').write_text('outside the course', encoding='utf-8')

        self._saved_env = {key: os.environ.get(key)
                           for key in ('OFFLINEU_PROGRESS_DIR', 'OFFLINEU_ROOTS')}
        os.environ['OFFLINEU_PROGRESS_DIR'] = str(self.progress_dir)
        os.environ.pop('OFFLINEU_ROOTS', None)

        core.course_store.clear()
        core.app.config['TESTING'] = True
        self.client = core.app.test_client()

    def tearDown(self):
        core.course_store.clear()
        for key, value in self._saved_env.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        shutil.rmtree(self.tmp_dir, ignore_errors=True)

    def write(self, relative, content):
        path = self.course_dir / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(content, bytes):
            path.write_bytes(content)
        else:
            path.write_text(content, encoding='utf-8')
        return path

    def load_course(self):
        response = self.client.post('/load_course', json={'course_path': str(self.course_dir)})
        self.assertEqual(response.status_code, 200, response.get_json())
        return core.course_store.get()

    def lessons(self):
        return core.get_all_lessons(core.course_store.get().root_node)

    def lessons_by_name(self):
        return {lesson.rel_path: lesson for lesson in self.lessons()}


    def progress_json(self):
        course = core.course_store.get()
        return json.loads(Path(course.progress_file).read_text(encoding='utf-8'))


class TestParsing(OfflineUTestCase):
    def test_builds_tree_and_detects_lesson_types(self):
        course = self.load_course()
        self.assertEqual(sorted(course.root_node.children), ['Section 1', 'Section 2'])

        section1 = course.root_node.children['Section 1']
        types = {lesson.title: lesson.lesson_type for lesson in section1.lessons}
        self.assertEqual(types['Intro'], 'video')
        self.assertEqual(types['Notes'], 'text')
        self.assertEqual(types['Quiz'], 'quiz')
        self.assertEqual(types['Slides'], 'text')
        self.assertEqual(types['Handout'], 'text')

    def test_urls_and_relative_paths_are_stable(self):
        self.load_course()
        lessons = self.lessons_by_name()
        self.assertIn('Section 1/01 - Intro.mp4', lessons)
        self.assertEqual(lessons['Section 1/01 - Intro.mp4'].url, 'Section 1/01 - Intro.mp4/Intro')
        self.assertEqual(lessons['Section 1/01 - Intro.mp4'].title, 'Intro')
        self.assertEqual(lessons['Section 2/resources/extras.md'].url,
                         'Section 2/resources/extras.md/Extras')

    def test_hidden_and_unsupported_files_are_ignored(self):
        self.load_course()
        paths = self.lessons_by_name()
        self.assertTrue(all('.hidden' not in path for path in paths))
        self.assertTrue(all(not path.endswith('.zip') for path in paths))
        # subtitle files become part of a lesson instead of a lesson of their own
        self.assertTrue(all(not path.lower().endswith('.srt') for path in paths))

    def test_subtitle_is_attached_to_the_video(self):
        self.load_course()
        lesson = self.lessons_by_name()['Section 1/01 - Intro.mp4']
        self.assertEqual(lesson.subtitle_file, 'Section 1/01 - Intro.srt')
        self.assertEqual(lesson.subtitle_src, '/subtitles/Section%201/01%20-%20Intro.srt')

    def test_mime_types_and_encoded_sources(self):
        self.load_course()
        lesson = self.lessons_by_name()['Section 1/01 - Intro.mp4']
        self.assertEqual(lesson.video_mime, 'video/mp4')
        self.assertEqual(lesson.video_src, '/files/Section%201/01%20-%20Intro.mp4')

    def test_document_modes(self):
        self.load_course()
        lessons = self.lessons_by_name()
        modes = {resource['name']: resource['mode']
                 for resource in core.build_text_resources(lessons['Section 1/04 - Slides.pdf'])}
        self.assertEqual(modes['04 - Slides.pdf'], 'iframe')

        modes = {resource['name']: resource['mode']
                 for resource in core.build_text_resources(lessons['Section 1/05 - Handout.docx'])}
        self.assertEqual(modes['05 - Handout.docx'], 'download')

        modes = {resource['name']: resource['mode']
                 for resource in core.build_text_resources(lessons['Section 1/02 - Notes.txt'])}
        self.assertEqual(modes['02 - Notes.txt'], 'text')

    def test_srt_is_converted_to_webvtt(self):
        vtt = core.convert_srt_to_vtt(SRT_SAMPLE)
        self.assertTrue(vtt.startswith('WEBVTT'))
        self.assertIn('00:00:01.000 --> 00:00:04.000', vtt)
        self.assertIn('Hello OfflineU', vtt)
        self.assertNotIn('\n1\n', vtt.replace('\r', ''))

    def test_resolve_inside_blocks_directory_escapes(self):
        self.assertEqual(core._resolve_inside(self.course_dir, '../secret.txt'), None)
        self.assertEqual(core._resolve_inside(self.course_dir, r'..\secret.txt'), None)
        inside = core._resolve_inside(self.course_dir, 'Section 1/02 - Notes.txt')
        self.assertEqual(inside, self.course_dir / 'Section 1' / '02 - Notes.txt')


class TestProgress(OfflineUTestCase):
    def test_opening_a_lesson_does_not_reset_completion(self):
        """Regression: visiting a finished lesson used to wipe completed/seconds."""
        self.load_course()
        url = 'Section 1/01 - Intro.mp4/Intro'
        self.client.post('/api/progress',
                         json={'lesson_path': url, 'completed': True, 'progress_seconds': 42})
        entry = self.progress_json()['Section 1/01 - Intro.mp4']
        self.assertTrue(entry['completed'])
        self.assertEqual(entry['progress_seconds'], 42)

        self.assertEqual(self.client.get('/lesson/' + url).status_code, 200)

        entry = self.progress_json()['Section 1/01 - Intro.mp4']
        self.assertTrue(entry['completed'])
        self.assertEqual(entry['progress_seconds'], 42)
        self.assertIn('lesson-item completed', self.client.get('/').get_data(as_text=True))

    def test_periodic_save_keeps_completion_and_never_rewinds(self):
        self.load_course()
        url = 'Section 1/01 - Intro.mp4/Intro'
        self.client.post('/api/progress',
                         json={'lesson_path': url, 'completed': True, 'progress_seconds': 120})
        # a timeupdate save without the completed flag and with a smaller position
        self.client.post('/api/progress', json={'lesson_path': url, 'progress_seconds': 30})
        entry = self.progress_json()['Section 1/01 - Intro.mp4']
        self.assertTrue(entry['completed'])
        self.assertEqual(entry['progress_seconds'], 120)

    def test_periodic_save_does_not_create_a_completed_key(self):
        self.load_course()
        self.client.post('/api/progress',
                         json={'lesson_path': 'Section 1/01 - Intro.mp4/Intro',
                               'progress_seconds': 15})
        self.assertNotIn('completed', self.progress_json()['Section 1/01 - Intro.mp4'])

    def test_progress_accepts_urls_and_relative_paths(self):
        self.load_course()
        for lesson_path in ('Section 1/01 - Intro.mp4/Intro', 'Section 1/01 - Intro.mp4'):
            response = self.client.post('/api/progress', json={'lesson_path': lesson_path})
            self.assertEqual(response.status_code, 200)
            self.assertEqual(response.get_json()['lesson_key'], 'Section 1/01 - Intro.mp4')
        self.assertIn('Section 1/01 - Intro.mp4', self.progress_json())

    def test_saved_progress_is_applied_to_the_tree(self):
        course = self.load_course()
        Path(course.progress_file).parent.mkdir(parents=True, exist_ok=True)
        Path(course.progress_file).write_text(json.dumps({
            'Section 1/01 - Intro.mp4/Intro': {'completed': True, 'progress_seconds': 180},
            'last_accessed_path': 'Section 1/01 - Intro.mp4/Intro',
        }), encoding='utf-8')

        core.ProgressTracker.apply_progress_to_tree(course)
        lesson = self.lessons_by_name()['Section 1/01 - Intro.mp4']
        self.assertTrue(lesson.completed)
        self.assertEqual(lesson.progress_seconds, 180)
        self.assertEqual(course.last_accessed_url, 'Section 1/01 - Intro.mp4/Intro')
        self.assertEqual(course.last_accessed_title, 'Intro')

    def test_progress_is_stored_outside_the_course_folder_when_configured(self):
        course = self.load_course()
        self.assertTrue(course.progress_file.startswith(str(self.progress_dir)))
        self.client.post('/api/progress',
                         json={'lesson_path': 'Section 1/02 - Notes.txt/Notes',
                               'progress_seconds': 5})
        self.assertTrue(Path(course.progress_file).is_file())

    def test_unwritable_progress_location_is_reported(self):
        course = self.load_course()
        blocker = self.tmp_dir / 'blocker'
        blocker.write_text('not a directory', encoding='utf-8')
        course.progress_file = str(blocker / 'progress.json')

        response = self.client.post('/api/progress',
                                    json={'lesson_path': 'Section 1/01 - Intro.mp4/Intro',
                                          'progress_seconds': 10})
        self.assertEqual(response.status_code, 500)
        self.assertIn('Cannot write progress', response.get_json()['error'])

        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        self.assertIn('Progress could not be saved', html)


class TestRoutes(OfflineUTestCase):
    def test_index_without_course_shows_the_directory_browser(self):
        html = self.client.get('/').get_data(as_text=True)
        self.assertIn('Select a Course', html)
        self.assertIn('browser-list', html)
        self.assertNotIn('<div class="tree-item">', html)

    def test_dashboard_renders_tree_links_and_continue_card(self):
        course = self.load_course()
        self.client.post('/api/progress',
                         json={'lesson_path': 'Section 1/01 - Intro.mp4/Intro',
                               'completed': True, 'progress_seconds': 90})
        html = self.client.get('/').get_data(as_text=True)

        self.assertIn(course.name, html)
        self.assertIn('Course Root', html)
        self.assertIn('<div class="tree-item">', html)
        self.assertNotIn('&lt;div', html)  # macros must not be escaped twice
        self.assertIn('href="/lesson/Section 1/01 - Intro.mp4/Intro"', html)
        self.assertIn('class="lesson-item completed"', html)
        self.assertIn('<strong>1/7</strong>', html)
        self.assertIn('Continue:', html)
        self.assertIn('href="/lesson/Section 1/01 - Intro.mp4/Intro" class="btn btn-small"',
                      html.replace('\n', ' ').replace('  ', ' '))

    def test_lesson_page_renders_player_subtitles_and_neighbours(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        self.assertIn('id="video-player"', html)
        self.assertIn('type="video/mp4"', html)
        self.assertIn('/subtitles/Section%201/01%20-%20Intro.srt', html)
        self.assertIn('href="/lesson/Section 1/02 - Notes.txt/Notes"', html)

        html = self.client.get('/lesson/Section 1/02 - Notes.txt/Notes').get_data(as_text=True)
        self.assertIn('data-text-src="/files/Section%201/02%20-%20Notes.txt"', html)
        self.assertIn('href="/lesson/Section 1/01 - Intro.mp4/Intro"', html)
        self.assertIn('href="/lesson/Section 1/03 - Quiz.html/Quiz"', html)

    def test_lesson_page_marks_completed_state_and_keeps_it(self):
        self.load_course()
        self.client.post('/api/progress',
                         json={'lesson_path': 'Section 1/01 - Intro.mp4/Intro', 'completed': True})
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        self.assertIn('Completed ✓', html)
        self.assertIn('const initialCompleted = true;', html)

    def test_lesson_deep_link_reflects_stored_progress(self):
        self.load_course()
        self.client.post('/api/progress',
                         json={'lesson_path': 'Section 1/01 - Intro.mp4/Intro',
                               'completed': True, 'progress_seconds': 125})
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        self.assertIn('const savedProgress = 125;', html)
        self.assertIn('const initialCompleted = true;', html)

    def test_lesson_page_marks_documents_that_cannot_be_previewed(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/05 - Handout.docx/Handout').get_data(as_text=True)
        self.assertIn('cannot be previewed in the browser', html)

    def test_unknown_lesson_redirects_home(self):
        self.load_course()
        self.assertEqual(self.client.get('/lesson/does/not/exist').status_code, 302)

    def test_files_are_served_with_the_right_mime_type(self):
        self.load_course()
        response = self.client.get('/files/Section%201/01%20-%20Intro.mp4')
        self.assertEqual(response.status_code, 200)
        self.assertTrue(response.headers['Content-Type'].startswith('video/mp4'))

        response = self.client.get('/files/Section%201/02%20-%20Notes.txt')
        self.assertEqual(response.get_data(as_text=True), 'Hello OfflineU')

    def test_file_and_subtitle_traversal_is_blocked(self):
        self.load_course()
        self.assertEqual(self.client.get('/files/../secret.txt').status_code, 403)
        self.assertEqual(self.client.get('/files/%2e%2e%2fsecret.txt').status_code, 403)
        self.assertEqual(self.client.get('/subtitles/../secret.txt').status_code, 403)

    def test_subtitle_endpoint_converts_srt_to_vtt(self):
        self.load_course()
        response = self.client.get('/subtitles/Section%201/01%20-%20Intro.srt')
        self.assertEqual(response.status_code, 200)
        self.assertTrue(response.headers['Content-Type'].startswith('text/vtt'))
        body = response.get_data(as_text=True)
        self.assertTrue(body.startswith('WEBVTT'))
        self.assertIn('00:00:01.000 --> 00:00:04.000', body)

    def test_health_and_reset_course(self):
        self.assertEqual(self.client.get('/health').get_json(), {'status': 'healthy'})
        self.load_course()
        self.assertEqual(self.client.get('/reset_course').status_code, 302)
        self.assertIsNone(core.course_store.get())
        self.assertIn('Select a Course', self.client.get('/').get_data(as_text=True))

    def test_browse_lists_course_candidates(self):
        payload = self.client.get('/browse?path=' + str(self.tmp_dir)).get_json()
        entry = next(item for item in payload['directories'] if item['name'] == 'Python Tutorial')
        self.assertTrue(entry['is_course_candidate'])
        self.assertGreaterEqual(entry['media_files'], 1)
        self.assertEqual(payload['parent_path'], str(self.tmp_dir.parent))


class TestSecurityAndCli(OfflineUTestCase):
    def test_roots_confine_browsing_and_loading(self):
        os.environ['OFFLINEU_ROOTS'] = str(self.course_dir)

        self.assertEqual(self.client.get('/browse?path=' + str(self.tmp_dir)).status_code, 403)
        self.assertEqual(self.client.post('/load_course',
                                          json={'course_path': str(self.tmp_dir)}).status_code, 403)

        payload = self.client.get('/browse').get_json()
        self.assertEqual(payload['current_path'], str(self.course_dir))
        self.assertIsNone(payload['parent_path'])  # cannot climb above the root

        response = self.client.post('/load_course', json={'course_path': str(self.course_dir)})
        self.assertEqual(response.status_code, 200)

    def test_loading_an_invalid_path_returns_400(self):
        self.assertEqual(self.client.post('/load_course', json={}).status_code, 400)
        self.assertEqual(self.client.post('/load_course',
                                          json={'course_path': str(self.tmp_dir / 'nope')}).status_code,
                         400)

    def test_progress_without_a_course_returns_400(self):
        response = self.client.post('/api/progress', json={'lesson_path': 'anything'})
        self.assertEqual(response.status_code, 400)
        self.assertEqual(self.client.post('/api/progress', json={}).status_code, 400)

    def test_files_without_a_course_returns_404(self):
        self.assertEqual(self.client.get('/files/whatever.txt').status_code, 404)

    def test_check_templates_reports_success(self):
        self.assertEqual(core.main(['--create-templates']), 0)

    def test_main_rejects_a_missing_course_path(self):
        self.assertEqual(core.main([str(self.tmp_dir / 'missing')]), 1)

    def test_required_templates_are_present(self):
        self.assertEqual(core.missing_templates(), [])


class TestCourseMemory(OfflineUTestCase):
    """The course you added must survive going home, "switch course" and restarts."""

    def state_json(self):
        return json.loads(Path(core.course_store.state_file).read_text(encoding='utf-8'))

    def simulate_restart(self):
        """Throw away the in-memory course, exactly like a fresh process would."""
        core.course_store = core.CourseStore()

    def test_loading_a_course_is_remembered_on_disk(self):
        course = self.load_course()
        state_file = core.course_store.state_file
        self.assertTrue(str(state_file).startswith(str(self.tmp_dir)))
        self.assertTrue(state_file.is_file())

        state = self.state_json()
        self.assertEqual(state['active_course'], course.path)
        self.assertEqual([item['name'] for item in state['recent_courses']], [self.course_dir.name])

    def test_home_page_restores_the_course_after_a_restart(self):
        self.load_course()
        self.simulate_restart()
        self.assertIsNone(core.course_store.get())

        html = self.client.get('/').get_data(as_text=True)
        self.assertIn('<div class="tree-item">', html)
        self.assertEqual(core.course_store.get().path, str(self.course_dir))

    def test_going_back_to_the_home_page_keeps_the_course(self):
        self.load_course()
        self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro')
        html = self.client.get('/').get_data(as_text=True)
        self.assertIn('<div class="tree-item">', html)
        self.assertIn(self.course_dir.name, html)

    def test_reset_keeps_the_course_in_the_recent_list(self):
        self.load_course()
        self.assertEqual(self.client.get('/reset_course').status_code, 302)
        self.assertIsNone(core.course_store.get())

        html = self.client.get('/').get_data(as_text=True)
        self.assertIn('Recent courses', html)
        self.assertIn(self.course_dir.name, html)
        self.assertIn('recent-open', html)

        state = self.state_json()
        self.assertNotIn('active_course', state)
        self.assertEqual([item['name'] for item in state['recent_courses']], [self.course_dir.name])

        # one click is enough to get it back: no path typing required
        response = self.client.post('/load_course', json={'course_path': str(self.course_dir)})
        self.assertEqual(response.status_code, 200)
        self.assertIn('<div class="tree-item">', self.client.get('/').get_data(as_text=True))

    def test_courses_whose_folder_disappeared_are_not_offered(self):
        self.load_course()
        shutil.rmtree(self.course_dir)

        self.simulate_restart()
        self.assertEqual(core.course_store.recent_courses(), [])
        html = self.client.get('/').get_data(as_text=True)
        self.assertNotIn('Recent courses', html)
        self.assertNotIn('<div class="tree-item">', html)

    def test_restore_and_recent_list_respect_the_allowed_roots(self):
        self.load_course()
        elsewhere = self.tmp_dir / 'elsewhere'
        elsewhere.mkdir()
        os.environ['OFFLINEU_ROOTS'] = str(elsewhere)

        self.simulate_restart()
        html = self.client.get('/').get_data(as_text=True)
        self.assertIsNone(core.course_store.get())
        self.assertNotIn('<div class="tree-item">', html)
        self.assertEqual(core.course_store.recent_courses(), [])
        self.assertNotIn('Recent courses', html)

    def test_forget_course_removes_only_that_entry(self):
        self.load_course()
        self.assertEqual(
            self.client.get('/forget_course', query_string={'path': str(self.course_dir)}).status_code,
            302)
        self.assertEqual(core.course_store.recent_courses(), [])
        # the course that is currently open is not affected
        self.assertIsNotNone(core.course_store.get())
        self.assertIn('<div class="tree-item">', self.client.get('/').get_data(as_text=True))


class TestPlaybackPreferences(OfflineUTestCase):
    """Autoplay-next and the playback speed that follows you through the course."""

    PLAYER_TOOLBAR_MARKERS = ('id="playback-rate"', 'id="autoplay-next"',
                              'offlineu.playbackRate', 'offlineu.autoplayNext')

    def test_autoplay_targets_the_next_media_lesson_and_skips_documents(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        # 02..05 are documents, so the next *playable* lesson is the wrap-up video
        self.assertIn('/lesson/Section%201/06%20-%20Wrap%20Up.mp4/Wrap_Up?autoplay=1', html)
        self.assertNotIn('/lesson/Section%201/02%20-%20Notes.txt/Notes?autoplay=1', html)

    def test_autoplay_works_for_audio_lessons_too(self):
        self.write('Section 2/resources/01 - Recap.mp3', b'fake audio')
        self.load_course()
        html = self.client.get('/lesson/Section 1/06 - Wrap Up.mp4/Wrap_Up').get_data(as_text=True)
        self.assertIn('/lesson/Section%202/resources/01%20-%20Recap.mp3/Recap?autoplay=1', html)

    def test_autoplay_falls_back_to_the_next_lesson_after_the_last_media_file(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/06 - Wrap Up.mp4/Wrap_Up').get_data(as_text=True)
        self.assertIn('/lesson/Section%202/resources/extras.md/Extras?autoplay=1', html)

    def test_last_lesson_of_the_course_has_no_autoplay_target(self):
        self.load_course()
        html = self.client.get('/lesson/Section 2/resources/extras.md/Extras').get_data(as_text=True)
        self.assertIn('const autoplayHref = null;', html)
        self.assertIn('const autoplayTitle = null;', html)

    def test_autoplay_request_is_forwarded_to_the_player(self):
        self.load_course()
        lesson = '/lesson/Section%201/06%20-%20Wrap%20Up.mp4/Wrap_Up'
        self.assertIn('const autoplayRequested = false;',
                      self.client.get(lesson).get_data(as_text=True))
        self.assertIn('preload="metadata"', self.client.get(lesson).get_data(as_text=True))

        autoplay_page = self.client.get(lesson + '?autoplay=1').get_data(as_text=True)
        self.assertIn('const autoplayRequested = true;', autoplay_page)
        self.assertIn('preload="auto"', autoplay_page)

    def test_player_toolbar_is_only_rendered_for_media_lessons(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        for marker in self.PLAYER_TOOLBAR_MARKERS:
            self.assertIn(marker, html)
        self.assertIn('Up next:', html)
        self.assertIn('Autoplay next lesson', html)

        document_page = self.client.get('/lesson/Section 1/02 - Notes.txt/Notes').get_data(as_text=True)
        self.assertNotIn('id="playback-rate"', document_page)
        self.assertNotIn('id="autoplay-next"', document_page)

    def test_speed_is_applied_before_autoplay_starts(self):
        self.load_course()
        html = self.client.get('/lesson/Section 1/01 - Intro.mp4/Intro').get_data(as_text=True)
        # the stored rate is applied on load and re-applied in initPlayback (metadata known)
        self.assertIn("applyPlaybackRate(parseFloat(readPreference(RATE_KEY, '1')))", html)
        self.assertIn('applyPlaybackRate(activeMedia.playbackRate);', html)
        self.assertIn('startAutoplay(activeMedia);', html)


if __name__ == '__main__':
    unittest.main(verbosity=2)






