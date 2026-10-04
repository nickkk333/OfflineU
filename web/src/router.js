import { createRouter, createWebHistory } from 'vue-router'
import HomeView from './views/HomeView.vue'
import LessonView from './views/LessonView.vue'

// history mode: the Go server falls back to index.html for unknown paths, so
// deep links such as /lesson/Section%201/01%20-%20Intro.mp4/Intro work.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/lesson/:lessonPath(.*)*', name: 'lesson', component: LessonView },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ],
  scrollBehavior() {
    return { top: 0 }
  }
})
