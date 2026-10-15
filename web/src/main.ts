import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import ToolsView from './views/ToolsView.vue'
import RolesView from './views/RolesView.vue'
import ProjectsView from './views/ProjectsView.vue'
import ProjectView from './views/ProjectView.vue'
import './styles.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/projects' },
    { path: '/tools', component: ToolsView, meta: { title: '启动检查' } },
    { path: '/roles', component: RolesView, meta: { title: '角色分配' } },
    { path: '/projects', component: ProjectsView, meta: { title: '项目' } },
    { path: '/projects/:id', component: ProjectView, meta: { title: '项目' } },
    { path: '/:pathMatch(.*)*', redirect: '/projects' },
  ],
})
router.afterEach((to) => {
  document.title = `${(to.meta.title as string) ?? ''} · AutoCoder`
})

createApp(App).use(router).mount('#app')
