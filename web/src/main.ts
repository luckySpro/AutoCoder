import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import ToolsView from './views/ToolsView.vue'
import RolesView from './views/RolesView.vue'
import ProjectsView from './views/ProjectsView.vue'
import ProjectView from './views/ProjectView.vue'
import UsageView from './views/UsageView.vue'
import LiveView from './views/LiveView.vue'
import './styles.css'

const router = createRouter({
  // hash 路由：内嵌到单文件程序或作为静态页面预览时都无需服务端回落
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/projects' },
    { path: '/tools', component: ToolsView, meta: { title: '启动检查' } },
    { path: '/roles', component: RolesView, meta: { title: '角色分配' } },
    { path: '/projects', component: ProjectsView, meta: { title: '项目' } },
    { path: '/projects/:id', component: ProjectView, meta: { title: '项目' } },
    { path: '/live', component: LiveView, meta: { title: '运行监控' } },
    { path: '/usage', component: UsageView, meta: { title: 'Token 用量' } },
    { path: '/:pathMatch(.*)*', redirect: '/projects' },
  ],
})
router.afterEach((to) => {
  document.title = `${(to.meta.title as string) ?? ''} · AutoCoder`
})

createApp(App).use(router).mount('#app')
