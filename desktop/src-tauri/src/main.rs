// AutoCoder 桌面外壳：窗口、菜单栏（托盘）图标和后台运行。
//
// 业务都在内置的 Go 引擎（sidecar：autocoder-engine）里，外壳只做三件事：
//   1. 启动引擎并等它就绪，在窗口里打开 http://127.0.0.1:7788 的界面；
//   2. 菜单栏图标显示引擎给的状态（/api/desktop/menu），可暂停派发、打开界面、退出；
//   3. 关窗口只是隐藏，引擎在后台继续；退出时让引擎收尾（运行中的任务放回队列）再结束。
// 外壳意外结束时引擎的标准输入会关闭，引擎也会自己收尾退出。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::io::Read;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use serde::Deserialize;
use tauri::image::Image;
use tauri::menu::{Menu, MenuBuilder, MenuEvent, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIcon, TrayIconBuilder, TrayIconEvent};
use tauri::{
    AppHandle, Manager, RunEvent, Url, WebviewUrl, WebviewWindowBuilder, WindowEvent, Wry,
};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const ADDR: &str = "127.0.0.1:7788";
const BASE: &str = "http://127.0.0.1:7788";
const TRAY: &str = "main";
const WINDOW: &str = "main";

/// 引擎给的菜单内容（Go 里的 tray.Menu）。
#[derive(Deserialize, PartialEq, Clone, Default)]
#[serde(rename_all = "camelCase", default)]
struct TrayMenu {
    title: String,
    tooltip: String,
    header: String,
    #[serde(deserialize_with = "null_default")]
    running: Vec<Item>,
    wait_header: String,
    #[serde(deserialize_with = "null_default")]
    waiting: Vec<Item>,
    more: String,
    usage: String,
    #[serde(deserialize_with = "null_default")]
    tools: Vec<String>,
    pause: String,
    paused: bool,
    quit: String,
    icon: IconState,
}

/// 引擎的空列表可能是 null。
fn null_default<'de, D: serde::Deserializer<'de>, T: Default + Deserialize<'de>>(
    d: D,
) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

#[derive(Deserialize, PartialEq, Clone, Default)]
#[serde(default)]
struct Item {
    text: String,
    link: String,
}

#[derive(Deserialize, PartialEq, Clone, Default)]
#[serde(default)]
struct IconState {
    slots: i64,
    busy: i64,
    waiting: bool,
    paused: bool,
}

/// 外壳的运行状态。
struct Shell {
    token: String,
    own_engine: AtomicBool, // 引擎是这个外壳启动的；否则是已经在运行的 AutoCoder
    child: Mutex<Option<CommandChild>>, // 持有它，引擎的标准输入就一直开着
    engine_exited: AtomicBool,
    stderr_tail: Mutex<Vec<String>>,
    quitting: AtomicBool,
    links: Mutex<Vec<String>>,
    paused: AtomicBool,
    start_url: Mutex<Option<Url>>,
}

fn main() {
    let shell = Arc::new(Shell {
        token: new_token(),
        own_engine: AtomicBool::new(false),
        child: Mutex::new(None),
        engine_exited: AtomicBool::new(false),
        stderr_tail: Mutex::new(Vec::new()),
        quitting: AtomicBool::new(false),
        links: Mutex::new(Vec::new()),
        paused: AtomicBool::new(false),
        start_url: Mutex::new(None),
    });

    let app = tauri::Builder::default()
        // 再次打开 AutoCoder 时不起第二个，把已有的窗口拿到前面
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_window(app)
        }))
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .manage(shell.clone())
        .setup(move |app| {
            let handle = app.handle().clone();
            create_window(&handle)?;
            create_tray(&handle)?;
            start_engine(&handle, &shell)?;
            let s = shell.clone();
            thread::spawn(move || watch(handle, s));
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("AutoCoder 外壳启动失败");

    app.run(|app, event| match event {
        // 关窗口只是隐藏：引擎继续在后台派发任务
        RunEvent::WindowEvent {
            label,
            event: WindowEvent::CloseRequested { api, .. },
            ..
        } if label == WINDOW => {
            api.prevent_close();
            hide_window(app);
        }
        // Command-Q、系统注销等：先让引擎收尾
        RunEvent::ExitRequested { api, .. } => {
            let shell = app.state::<Arc<Shell>>();
            if !shell.quitting.load(Ordering::SeqCst) {
                api.prevent_exit();
                begin_quit(app.clone());
            }
        }
        #[cfg(target_os = "macos")]
        RunEvent::Reopen { .. } => show_window(app),
        _ => {}
    });
}

fn new_token() -> String {
    let mut b = [0u8; 24];
    getrandom::getrandom(&mut b).expect("无法生成随机数");
    b.iter().map(|x| format!("{x:02x}")).collect()
}

fn agent() -> ureq::Agent {
    ureq::AgentBuilder::new()
        .timeout_connect(Duration::from_millis(800))
        .timeout(Duration::from_secs(5))
        .build()
}

// ---------- 窗口 ----------

/// 把界面里 target=_blank 的链接和 window.open 交给外壳：用系统浏览器打开。
const INIT_SCRIPT: &str = r#"
(() => {
  const ext = (u) => { location.href = 'http://127.0.0.1:7788/__open?u=' + encodeURIComponent(new URL(u, location.href).href) };
  document.addEventListener('click', (e) => {
    const a = e.target && e.target.closest && e.target.closest('a[target="_blank"]');
    if (a && a.href) { e.preventDefault(); ext(a.href) }
  }, true);
  window.open = (u) => { if (u) ext(String(u)); return null };
})();
"#;

fn create_window(app: &AppHandle) -> tauri::Result<()> {
    let h = app.clone();
    let win = WebviewWindowBuilder::new(app, WINDOW, WebviewUrl::App("index.html".into()))
        .title("AutoCoder")
        .inner_size(1280.0, 820.0)
        .min_inner_size(420.0, 560.0)
        .initialization_script(INIT_SCRIPT)
        .on_navigation(move |url| allow_navigation(&h, url))
        .build()?;
    *app.state::<Arc<Shell>>().start_url.lock().unwrap() = win.url().ok();
    Ok(())
}

fn is_engine(url: &Url) -> bool {
    url.scheme() == "http"
        && matches!(url.host_str(), Some("127.0.0.1") | Some("localhost"))
        && url.port() == Some(7788)
}

/// 窗口里只显示 AutoCoder 自己的页面，其他网址用系统浏览器打开。
fn allow_navigation(app: &AppHandle, url: &Url) -> bool {
    if is_engine(url) && url.path() == "/__open" {
        if let Some((_, u)) = url.query_pairs().find(|(k, _)| k == "u") {
            open_external(app, &u);
        }
        return false;
    }
    if is_engine(url)
        || url.scheme() == "tauri"
        || url.host_str() == Some("tauri.localhost")
        || url.scheme() == "about"
    {
        return true;
    }
    if url.scheme() == "http" || url.scheme() == "https" {
        open_external(app, url.as_str());
    }
    false
}

fn open_external(_app: &AppHandle, u: &str) {
    let ok = Url::parse(u)
        .map(|x| x.scheme() == "http" || x.scheme() == "https")
        .unwrap_or(false);
    if ok {
        let _ = tauri_plugin_opener::open_url(u, None::<&str>);
    }
}

fn show_window(app: &AppHandle) {
    #[cfg(target_os = "macos")]
    let _ = app.set_activation_policy(tauri::ActivationPolicy::Regular);
    if let Some(w) = app.get_webview_window(WINDOW) {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

fn hide_window(app: &AppHandle) {
    if let Some(w) = app.get_webview_window(WINDOW) {
        let _ = w.hide();
    }
    // 窗口藏起来后 Dock 里也不留图标，只在菜单栏
    #[cfg(target_os = "macos")]
    let _ = app.set_activation_policy(tauri::ActivationPolicy::Accessory);
}

fn open_page(app: &AppHandle, link: &str) {
    if let (Some(w), Ok(u)) = (app.get_webview_window(WINDOW), Url::parse(link)) {
        if is_engine(&u) {
            let _ = w.navigate(u);
        }
    }
    show_window(app);
}

fn show_error(app: &AppHandle, msg: &str) {
    let shell = app.state::<Arc<Shell>>();
    let start = shell.start_url.lock().unwrap().clone();
    if let (Some(w), Some(mut u)) = (app.get_webview_window(WINDOW), start) {
        let enc: String = url_encode(msg);
        u.set_fragment(Some(&format!("error={enc}")));
        let _ = w.navigate(u);
    }
    show_window(app);
}

fn url_encode(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

// ---------- 引擎 ----------

fn engine_running() -> bool {
    agent()
        .get(&format!("{BASE}/api/version"))
        .call()
        .ok()
        .and_then(|r| r.into_json::<serde_json::Value>().ok())
        .map(|v| v["name"] == "autocoder")
        .unwrap_or(false)
}

fn start_engine(app: &AppHandle, shell: &Arc<Shell>) -> Result<(), Box<dyn std::error::Error>> {
    if engine_running() {
        // 已经有 AutoCoder 在运行（比如命令行版），直接用它
        return Ok(());
    }
    let (mut rx, child) = app
        .shell()
        .sidecar("autocoder-engine")?
        .args(["--addr", ADDR, "--desktop", "serve"])
        .env("AUTOCODER_DESKTOP_TOKEN", &shell.token)
        .spawn()?;
    shell.own_engine.store(true, Ordering::SeqCst);
    *shell.child.lock().unwrap() = Some(child);

    let h = app.clone();
    let s = shell.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(ev) = rx.recv().await {
            match ev {
                CommandEvent::Stderr(line) | CommandEvent::Stdout(line) => {
                    let mut t = s.stderr_tail.lock().unwrap();
                    t.push(String::from_utf8_lossy(&line).trim_end().to_string());
                    let n = t.len();
                    if n > 12 {
                        t.drain(..n - 12);
                    }
                }
                CommandEvent::Terminated(p) => {
                    s.engine_exited.store(true, Ordering::SeqCst);
                    if !s.quitting.load(Ordering::SeqCst) {
                        let tail = s.stderr_tail.lock().unwrap().join("\n");
                        show_error(&h, &format!("内置引擎意外退出（退出码 {:?}）。\n日志：~/.autocoder/autocoder.log\n\n{tail}", p.code));
                    }
                }
                _ => {}
            }
        }
    });
    Ok(())
}

/// 等引擎就绪后打开界面，然后每两秒刷新一次菜单栏。
fn watch(app: AppHandle, shell: Arc<Shell>) {
    let deadline = Instant::now() + Duration::from_secs(90);
    loop {
        if engine_running() {
            break;
        }
        if shell.engine_exited.load(Ordering::SeqCst) {
            return; // 已经显示了错误
        }
        if Instant::now() > deadline {
            show_error(
                &app,
                "等了 90 秒，内置引擎还没有就绪。\n日志：~/.autocoder/autocoder.log",
            );
            return;
        }
        thread::sleep(Duration::from_millis(300));
    }
    open_page(&app, &format!("{BASE}/"));

    let mut last: Option<TrayMenu> = None;
    let mut last_icon: Option<IconState> = None;
    while !shell.quitting.load(Ordering::SeqCst) {
        let menu = fetch_menu(&shell).unwrap_or_else(|| fallback_menu(&shell));
        if last.as_ref() != Some(&menu) {
            if last_icon.as_ref() != Some(&menu.icon) && shell.own_engine.load(Ordering::SeqCst) {
                set_icon(&app, &shell);
                last_icon = Some(menu.icon.clone());
            }
            apply_menu(&app, &shell, &menu);
            last = Some(menu);
        }
        thread::sleep(Duration::from_secs(2));
    }
}

fn fetch_menu(shell: &Shell) -> Option<TrayMenu> {
    if !shell.own_engine.load(Ordering::SeqCst) || shell.engine_exited.load(Ordering::SeqCst) {
        return None;
    }
    agent()
        .get(&format!("{BASE}/api/desktop/menu"))
        .set("X-AutoCoder-Token", &shell.token)
        .call()
        .ok()?
        .into_json()
        .ok()
}

fn fallback_menu(shell: &Shell) -> TrayMenu {
    let header = if !shell.own_engine.load(Ordering::SeqCst) {
        "已连接到另一个正在运行的 AutoCoder"
    } else if shell.engine_exited.load(Ordering::SeqCst) {
        "内置引擎已退出"
    } else {
        "正在连接内置引擎…"
    };
    TrayMenu {
        header: header.into(),
        tooltip: "AutoCoder".into(),
        quit: "退出 AutoCoder".into(),
        ..Default::default()
    }
}

fn set_icon(app: &AppHandle, shell: &Shell) {
    let template = cfg!(target_os = "macos");
    let url = format!(
        "{BASE}/api/desktop/icon.png?size={}&template={}",
        if template { 44 } else { 64 },
        template as u8
    );
    let Ok(resp) = agent()
        .get(&url)
        .set("X-AutoCoder-Token", &shell.token)
        .call()
    else {
        return;
    };
    let mut buf = Vec::new();
    if resp
        .into_reader()
        .take(1 << 20)
        .read_to_end(&mut buf)
        .is_err()
    {
        return;
    }
    if let (Some(tray), Ok(img)) = (app.tray_by_id(TRAY), Image::from_bytes(&buf)) {
        let _ = tray.set_icon(Some(img));
        let _ = tray.set_icon_as_template(template);
    }
}

// ---------- 菜单栏 ----------

fn create_tray(app: &AppHandle) -> tauri::Result<TrayIcon> {
    let menu = build_menu(
        app,
        &TrayMenu {
            header: "正在启动…".into(),
            quit: "退出 AutoCoder".into(),
            ..Default::default()
        },
    )?;
    let mut b = TrayIconBuilder::with_id(TRAY)
        .tooltip("AutoCoder")
        .menu(&menu)
        .on_menu_event(on_menu)
        .on_tray_icon_event(|tray, ev| {
            // Windows / Linux：左键单击图标直接打开窗口，右键看菜单
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = ev
            {
                if !cfg!(target_os = "macos") {
                    show_window(tray.app_handle());
                }
            }
        });
    #[cfg(not(target_os = "macos"))]
    {
        b = b.show_menu_on_left_click(false);
    }
    if let Some(icon) = app.default_window_icon() {
        b = b.icon(icon.clone());
    }
    b.build(app)
}

fn build_menu(app: &AppHandle, m: &TrayMenu) -> tauri::Result<Menu<Wry>> {
    let off = |id: &str, text: &str| MenuItem::with_id(app, id, text, false, None::<&str>);
    let on = |id: String, text: &str| MenuItem::with_id(app, id, text, true, None::<&str>);
    let mut b = MenuBuilder::new(app).item(&off("header", &m.header)?);
    let mut n = 0;
    for it in &m.running {
        b = b.item(&on(format!("link:{n}"), &it.text)?);
        n += 1;
    }
    if !m.wait_header.is_empty() {
        b = b.separator().item(&off("wait", &m.wait_header)?);
        for it in &m.waiting {
            b = b.item(&on(format!("link:{n}"), &it.text)?);
            n += 1;
        }
        if !m.more.is_empty() {
            b = b.item(&on("open".into(), &m.more)?);
        }
    }
    if !m.usage.is_empty() {
        b = b.separator().item(&off("usage", &m.usage)?);
        for (i, t) in m.tools.iter().enumerate() {
            b = b.item(&off(&format!("tool:{i}"), &format!("    {t}"))?);
        }
    }
    b = b.separator().item(&on("open".into(), "打开 AutoCoder")?);
    if !m.pause.is_empty() {
        b = b.item(&on("pause".into(), &m.pause)?);
    }
    b.item(&on("quit".into(), &m.quit)?).build()
}

fn apply_menu(app: &AppHandle, shell: &Shell, m: &TrayMenu) {
    *shell.links.lock().unwrap() = m
        .running
        .iter()
        .chain(m.waiting.iter())
        .map(|i| i.link.clone())
        .collect();
    shell.paused.store(m.paused, Ordering::SeqCst);
    let Some(tray) = app.tray_by_id(TRAY) else {
        return;
    };
    if let Ok(menu) = build_menu(app, m) {
        let _ = tray.set_menu(Some(menu));
    }
    let _ = tray.set_tooltip(Some(&m.tooltip));
    #[cfg(target_os = "macos")]
    let _ = tray.set_title(Some(&m.title)); // 菜单栏图标旁显示运行中的数量
}

fn on_menu(app: &AppHandle, ev: MenuEvent) {
    let shell = app.state::<Arc<Shell>>().inner().clone();
    match ev.id().as_ref() {
        "open" => open_page(app, &format!("{BASE}/")),
        "quit" => begin_quit(app.clone()),
        "pause" => {
            let next = !shell.paused.load(Ordering::SeqCst);
            thread::spawn(move || {
                let _ = agent()
                    .post(&format!("{BASE}/api/desktop/pause"))
                    .set("X-AutoCoder-Token", &shell.token)
                    .send_json(serde_json::json!({ "paused": next }));
            });
        }
        id => {
            if let Some(i) = id
                .strip_prefix("link:")
                .and_then(|s| s.parse::<usize>().ok())
            {
                let link = shell
                    .links
                    .lock()
                    .unwrap()
                    .get(i)
                    .cloned()
                    .unwrap_or_else(|| format!("{BASE}/"));
                open_page(app, &link);
            }
        }
    }
}

// ---------- 退出 ----------

/// 退出：引擎停止派发，运行中的任务保存进度放回队列，下次启动继续；最多等 30 秒。
fn begin_quit(app: AppHandle) {
    let shell = app.state::<Arc<Shell>>().inner().clone();
    if shell.quitting.swap(true, Ordering::SeqCst) {
        return;
    }
    if let Some(w) = app.get_webview_window(WINDOW) {
        let _ = w.hide();
    }
    if let Some(tray) = app.tray_by_id(TRAY) {
        let busy = TrayMenu {
            header: "正在退出，运行中的任务保存进度…".into(),
            ..Default::default()
        };
        if let Ok(menu) = MenuBuilder::new(&app)
            .item(&MenuItem::with_id(&app, "header", &busy.header, false, None::<&str>).unwrap())
            .build()
        {
            let _ = tray.set_menu(Some(menu));
        }
        let _ = tray.set_tooltip(Some("AutoCoder 正在退出…"));
    }
    thread::spawn(move || {
        if shell.own_engine.load(Ordering::SeqCst) && !shell.engine_exited.load(Ordering::SeqCst) {
            let asked = agent()
                .post(&format!("{BASE}/api/desktop/quit"))
                .set("X-AutoCoder-Token", &shell.token)
                .call()
                .is_ok();
            let deadline = Instant::now() + Duration::from_secs(30);
            while asked && !shell.engine_exited.load(Ordering::SeqCst) && Instant::now() < deadline
            {
                thread::sleep(Duration::from_millis(200));
            }
            if !shell.engine_exited.load(Ordering::SeqCst) {
                if let Some(child) = shell.child.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        }
        app.exit(0);
    });
}
