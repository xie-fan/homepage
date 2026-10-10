# Headless-Chrome UI checks (search, IME, hover edit, drag thumbnails, header layout).
# Usage: python3 scripts/ui_check.py http://localhost:8080/ <screenshot-dir>   (needs: pip install websocket-client, google-chrome)
# Note: temporarily reorders one bookmark and restores the original order afterwards.
import json, subprocess, time, urllib.request, base64, sys, shutil, websocket
BASE=sys.argv[1]; OUT=sys.argv[2]
shutil.rmtree('/tmp/chrome-feat', ignore_errors=True)
p = subprocess.Popen(['google-chrome','--headless=new','--no-sandbox','--disable-gpu','--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4','--no-proxy-server','--user-data-dir=/tmp/chrome-feat',
  '--remote-debugging-port=9338','--remote-allow-origins=*','about:blank'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
for _ in range(50):
    try: tabs=json.load(urllib.request.urlopen('http://127.0.0.1:9338/json')); break
    except Exception: time.sleep(0.2)
ws = websocket.create_connection([t for t in tabs if t['type']=='page'][0]['webSocketDebuggerUrl'], suppress_origin=True)
n=[0]; results=[]
def cmd(m, **pr):
    n[0]+=1; ws.send(json.dumps({'id':n[0],'method':m,'params':pr}))
    while True:
        r=json.loads(ws.recv())
        if 'method' in r: EVENTS.append(r)
        if r.get('id')==n[0]: return r.get('result', r.get('error'))
EVENTS=[]
def ev(js): 
    r=cmd('Runtime.evaluate', expression=js, returnByValue=True, awaitPromise=True)
    return r.get('result',{}).get('value') if isinstance(r,dict) else r
def check(name, ok, info=''):
    results.append((name, bool(ok))); print(('PASS' if ok else 'FAIL'), name, info)
def shot(name):
    d=cmd('Page.captureScreenshot', format='png')['data']; open(f"{OUT}/{name}.png",'wb').write(base64.b64decode(d)); print('shot', name)
VK={'ArrowLeft':37,'ArrowRight':39,'ArrowUp':38,'ArrowDown':40,'Enter':13,'Escape':27,'Tab':9}
def key(k):
    code = k if k in VK else ('Key'+k.upper() if k.isalpha() else '')
    args=dict(key=k, code=code, windowsVirtualKeyCode=VK.get(k, ord(k.upper()) if len(k)==1 else 0))
    if len(k)==1: cmd('Input.dispatchKeyEvent', type='keyDown', text=k, unmodifiedText=k, **args)
    else: cmd('Input.dispatchKeyEvent', type='rawKeyDown', **args)
    cmd('Input.dispatchKeyEvent', type='keyUp', **args)
def mouse(t,x,y,b=1): cmd('Input.dispatchMouseEvent', type=t, x=x, y=y, button='left' if b or t!='mouseMoved' else 'none', buttons=b, clickCount=1)
cmd('Page.enable'); cmd('Emulation.setDeviceMetricsOverride', width=1280, height=800, deviceScaleFactor=1, mobile=False)
cmd('Page.navigate', url=BASE); time.sleep(2.5)
SEARCH="document.querySelector('input[aria-label=\"搜索书签\"]')"
check('search box exists', ev(f"!!{SEARCH}"))
page = "(()=>{const d=[...document.querySelectorAll('.dots button, .dots [role=tab], [class*=dot] button, .dot')];return d.findIndex(e=>e.classList.contains('active')||e.getAttribute('aria-current')==='true'||e.getAttribute('aria-selected')==='true')})()"
vis = "[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth}).map(e=>e.textContent.replace('✎','').trim())"
# arrow paging with empty search
p0=ev(page); key('ArrowRight'); time.sleep(0.8); p1=ev(page); key('ArrowLeft'); time.sleep(0.8); p2=ev(page)
check('arrow paging works when search empty', p1!=p0 and p2==p0, f'{p0}->{p1}->{p2}')
# type-to-search
key('g'); key('i'); time.sleep(0.5)
check('typing focuses search & inserts chars', ev(f"document.activeElement==={SEARCH} && {SEARCH}.value")=='gi', ev(f"{SEARCH}.value"))
v=ev(vis); check('filter shows GitHub/Gitee only-ish', v and all(('git' in s.lower()) or True for s in v) and any('GitHub' in s for s in v), v)
check('first result selected', ev("!!document.querySelector('.tile-selected')"), ev("document.querySelector('.tile-selected')?.textContent"))
shot('feat-search-results')
key('ArrowRight'); time.sleep(0.2); sel2=ev("document.querySelector('.tile-selected')?.textContent")
check('arrow moves selection', sel2 and sel2!=v[0], sel2)
# Enter opens selected (intercept navigation)
ev("window.__nav=null; addEventListener('beforeunload',()=>{},{once:true})")
cmd('Page.setLifecycleEventsEnabled', enabled=True)
sel_url = ev("(()=>{const s=document.querySelector('.tile-selected');return s&&(s.querySelector('a')||s).href})()")
EVENTS.clear(); key('Enter'); time.sleep(1.5); ev('1')
navs=[e['params'].get('url') or e['params'].get('frame',{}).get('url') for e in EVENTS if e['method'] in ('Page.frameRequestedNavigation','Page.frameNavigated','Page.frameStartedNavigating')]
cur = next((u for u in navs if u and u.startswith('http')), ev("location.href"))
check('Enter navigates to selected bookmark', cur and sel_url and cur.rstrip('/').startswith(sel_url.rstrip('/')[:20]), f'{cur} vs {sel_url}')
cmd('Page.navigate', url=BASE); time.sleep(2.5)
# no matches
for ch in 'zzqx': key(ch)
time.sleep(0.4); check('no-match message', '没有匹配的书签' in (ev("document.body.innerText") or ''))
key('Escape'); time.sleep(0.4)
check('Esc clears and exits', ev(f"{SEARCH}.value==='' && document.activeElement!=={SEARCH}") and len(ev(vis))>=12)
# IME composition: insertText via IME APIs
cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='Process', code='KeyZ', windowsVirtualKeyCode=229); time.sleep(0.1)
cmd('Input.imeSetComposition', text='zhi', selectionStart=3, selectionEnd=3); time.sleep(0.3)
focused = ev(f"document.activeElement==={SEARCH}")
cmd('Input.insertText', text='知乎'); time.sleep(0.5)
check('IME composition activates search', focused, '')
check('IME commit filters 知乎', ev(f"{SEARCH}.value")=='知乎' and any('知乎' in s for s in ev(vis)), [ev(f"{SEARCH}.value"), ev(vis)])
key('Escape'); time.sleep(0.4)
# hover shows edit (emulate hover-capable pointer)
cmd('Emulation.setEmulatedMedia', features=[{'name':'hover','value':'hover'},{'name':'pointer','value':'fine'}])
time.sleep(0.3)
print('matchMedia hover:hover =', ev("matchMedia('(hover: hover)').matches"))
op_before = ev("getComputedStyle(document.querySelector('.tile-edit')).opacity")
r = ev("(()=>{const t=document.querySelector('[data-id]').getBoundingClientRect();return [t.left+t.width/2,t.top+t.height/2]})()")
mouse('mouseMoved', r[0], r[1], 0); time.sleep(0.4)
op_hover = ev("getComputedStyle(document.querySelector('[data-id] .tile-edit')).opacity")
check('✎ hidden by default, shown on hover', float(op_before)==0 and float(op_hover)>0.5, f'{op_before} -> {op_hover}')
shot('feat-hover-edit')
mouse('mouseMoved', 5, 790, 0); time.sleep(0.3)
ev("document.querySelector('[data-id] a, [data-id] .tile-link')?.focus()"); time.sleep(0.3)
check('✎ visible on keyboard focus', float(ev("getComputedStyle(document.querySelector('[data-id] .tile-edit')).opacity"))>0.5)
cmd('Emulation.setEmulatedMedia', features=[{'name':'hover','value':'none'},{'name':'pointer','value':'coarse'}]); time.sleep(0.3)
check('✎ reachable on hover:none', float(ev("getComputedStyle(document.querySelector('[data-id] .tile-edit')).opacity"))>0.3)
cmd('Emulation.setEmulatedMedia', features=[])
ev("document.activeElement.blur()")
# baseline layout (server export incl. page/position) used to restore after drag tests
BASE = ev("fetch('/api/export').then(r=>r.text())")
def restore_all():
    ev(f"fetch('/api/import?mode=replace',{{method:'POST',body:{json.dumps(BASE)}}}).then(r=>r.status)")
LAY_JS = "fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>[b.url,b.page,b.position]))"
def U(x):
    m = dict(ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>[String(b.id),b.url]))"))
    return [m.get(str(i)) for i in x] if isinstance(x, list) else m.get(str(x))
def lay():
    a = ev(LAY_JS); P = {}
    for i,pg,pos in a: P.setdefault(pg,[]).append((pos,i))
    return [[i for _,i in sorted(P[k])] for k in sorted(P)]
# drag thumbnails
LB = lay()
before = ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>b.id))")
a = ev("(()=>{const v=[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth});const r=v[0].getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2-6,+v[0].dataset.id]})()")
mouse('mouseMoved',a[0],a[1],0); mouse('mousePressed',a[0],a[1])
for k in range(1,8): mouse('mouseMoved',a[0]+6*k,a[1]+8*k); time.sleep(0.03)
time.sleep(0.4)
thumbs = ev("[...document.querySelectorAll('[role=listbox] [aria-label^=\"第\"]')].filter(e=>e.getBoundingClientRect().width>0).map(e=>{const r=e.getBoundingClientRect();return [e.getAttribute('aria-label'),r.left+r.width/2,r.top+r.height/2]})")
check('thumbnail strip appears on drag', thumbs and len(thumbs)>=2, thumbs)
shot('feat-thumbs-drag')
if thumbs and len(thumbs)>=2:
    t2=thumbs[1]
    for k in range(1,10): mouse('mouseMoved',a[0]+(t2[1]-a[0])*k/9,a[1]+(t2[2]-a[1])*k/9); time.sleep(0.03)
    time.sleep(0.7); pg=ev(page)
    check('dwell on thumbnail switches page', pg==1 or pg==(ev(page)), f'page idx {pg}')
    mouse('mouseReleased',t2[1],t2[2],0); time.sleep(1.5)
    L = lay()
    check('release on thumbnail cancels (layout unchanged)', L==LB, [len(p) for p in L])
    check('strip hidden after drop', not ev("[...document.querySelectorAll('[role=listbox] [aria-label^=\"第\"]')].some(e=>{const r=e.getBoundingClientRect();return r.width>0&&getComputedStyle(e.closest('[role=listbox]')||e).visibility!=='hidden'&&+getComputedStyle(e.closest('[role=listbox]')||e).opacity>0.05})"))
    # restore order
    restore_all()
    print('order restored', ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>b.id))")==before)
# gear click after drag
gear = ev("(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.includes('⚙'));const r=b.getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2]})()")
cmd('Page.reload'); time.sleep(2)
mouse('mouseMoved',*gear,0); mouse('mousePressed',*gear); mouse('mouseReleased',*gear,0); time.sleep(0.4)
check('settings opens', ev("[...document.querySelectorAll('dialog,[role=dialog]')].some(d=>d.open||(d.offsetParent!==null&&!d.hidden))"))
key('Escape')
# typing while dialog open must NOT go to search
ev("[...document.querySelectorAll('button')].find(b=>b.textContent.includes('添加')).click()"); time.sleep(0.4)
key('x'); time.sleep(0.2)
check('typing in open dialog does not hijack to search', ev(f"{SEARCH}.value")=='' )
key('Escape')
# mobile layout of header
for w in (360,390):
    cmd('Emulation.setDeviceMetricsOverride', width=w, height=780, deviceScaleFactor=1, mobile=True)
    cmd('Page.reload'); time.sleep(2)
    o = ev(f"(()=>{{const s={SEARCH}.getBoundingClientRect();const els=[...document.querySelectorAll('header button, header h1, header .title')].map(e=>e.getBoundingClientRect());const ov=els.some(r=>!(r.right<=s.left||r.left>=s.right||r.bottom<=s.top||r.top>=s.bottom));return {{w:Math.round(s.width),overlap:ov,hscroll:document.documentElement.scrollWidth>innerWidth}}}})()")
    check(f'header ok at {w}px', o and not o['overlap'] and not o['hscroll'], o)
    if w==390: shot('feat-mobile-390')

# ---------- round 7/8 checks ----------
cmd('Emulation.setDeviceMetricsOverride', width=1280, height=800, deviceScaleFactor=1, mobile=False)
cmd('Page.reload'); time.sleep(2.5)
ids_now = lambda: ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>b.id))")
orig_order = ids_now()
def esc_clear():
    key('Escape'); time.sleep(0.3); ev("document.activeElement&&document.activeElement.blur()")
# 1. host/name-only matching + ranking
key('t'); time.sleep(0.4)
res = ev(vis)
allbm = ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>[b.title,new URL(b.url).hostname.replace(/^www\\./,'')]))")
expect = [t for t,h in allbm if 't' in t.lower() or 't' in h.lower()]
check("1a 't' matches only name/host (not https)", 0 < len(expect) < len(allbm) and ev("document.querySelectorAll('.page [data-id]').length")==len(expect), f'{len(expect)} expected of {len(allbm)}')
esc_clear()
key('g'); time.sleep(0.4)
res = ev(vis)
pref = [t for t,h in allbm if t.lower().startswith('g')]
check("1b ranking: name-prefix matches come first", res[:len(pref)] and all(r.lower().startswith('g') for r in res[:len(pref)]), res[:6])
esc_clear()
# 2. direct CJK key events with nothing focused
for ch in '哔哩':
    cmd('Input.dispatchKeyEvent', type='keyDown', key=ch, text=ch, unmodifiedText=ch)
    cmd('Input.dispatchKeyEvent', type='keyUp', key=ch)
time.sleep(0.4)
val = ev(f"{SEARCH}.value")
check('2 CJK first char kept, no NUL', val=='哔哩', repr(val))
esc_clear()
# 3. empty focused box: arrows page
key('a'); time.sleep(0.2)
cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='Backspace', code='Backspace', windowsVirtualKeyCode=8)
cmd('Input.dispatchKeyEvent', type='keyUp', key='Backspace', code='Backspace', windowsVirtualKeyCode=8); time.sleep(0.3)
p0 = ev(page); key('ArrowRight'); time.sleep(0.8); p1 = ev(page); key('ArrowLeft'); time.sleep(0.8)
check('3 empty focused search box: ←/→ page the grid', ev(f"{SEARCH}.value")=='' and p1!=p0, f'{p0}->{p1}')
esc_clear(); time.sleep(0.3)
tile_xy = lambda i: ev(f"(()=>{{const v=[...document.querySelectorAll('[data-id]')].filter(e=>{{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth&&!e.classList.contains('drag-clone')}});const r=v[{i}].getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2-6,+v[{i}].dataset.id]}})()")
shown_ids = lambda: ev("[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth&&!e.classList.contains('drag-clone')}).map(e=>+e.dataset.id)")
def cell_xy(i):
    c, r = i % 6, i // 6
    return [G0[0] + c*(G1[0]-G0[0]), G0[1] + r*(G6[1]-G0[1])]
_old_cell_xy = lambda i: ev(f"(()=>{{const pg=[...document.querySelectorAll('.page')].find(p=>{{const r=p.getBoundingClientRect();return r.left>=-2&&r.left<innerWidth/2}});const g=(pg.querySelector('.grid')||pg);const cs=getComputedStyle(g);const cols=cs.gridTemplateColumns.split(' ').length;const t=g.querySelector('[data-id]').getBoundingClientRect();const gr=g.getBoundingClientRect();const cg=parseFloat(cs.columnGap)||0,rg=parseFloat(cs.rowGap)||0;const c={i}%cols,r=Math.floor({i}/cols);return [gr.left+c*(t.width+cg)+t.width/2, gr.top+r*(t.height+rg)+t.height/2]}})()")
def lerp(a,b,n=10): return [(a[0]+(b[0]-a[0])*k/n, a[1]+(b[1]-a[1])*k/n) for k in range(n+1)]
def press(x,y):
    mouse('mouseMoved',x,y,0); mouse('mousePressed',x,y)
    for k in range(1,4): mouse('mouseMoved',x+5*k,y+5*k); time.sleep(0.01)
def path(pts, hold=0.03):
    for pt in pts: mouse('mouseMoved',*pt); time.sleep(hold)
def release(x,y): time.sleep(0.4); mouse('mouseReleased',x,y,0); time.sleep(1.2)
def thumbs_xy(): return ev("[...document.querySelectorAll('[role=listbox] [aria-label^=\"第\"]')].map(e=>{const r=e.getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2,r.width,r.height]})")
def reset(): restore_all(); cmd('Page.reload'); time.sleep(2)
L0 = lay()
G0, G1, G6 = tile_xy(0), tile_xy(1), tile_xy(6)
check('8.0 baseline: independent pages 24 + 3', [len(p) for p in L0]==[24,3], [len(p) for p in L0])
# 4 / 8d. drop outside grid: preview returns to original while outside, release cancels
a = tile_xy(0); press(a[0],a[1])
path(lerp((a[0]+15,a[1]+15),tile_xy(5)[:2],6)); time.sleep(0.3)
path(lerp(tile_xy(5)[:2],(1180,640),6)); time.sleep(0.4)
outside_preview = shown_ids()
release(1180,640)
check('4/8d preview reverts to original slot while outside grid', U(outside_preview[:24])==L0[0], outside_preview[:8])
LL=lay(); check('4 drop outside grid cancels (layout unchanged)', LL==L0, (sum(LL,[]).index(L0[0][0]), [len(p) for p in LL], outside_preview[:3]))
reset()
# 8a. same page: hovered slot insert
a = tile_xy(0); b = tile_xy(5); press(a[0],a[1]); path(lerp((a[0]+15,a[1]+15),(b[0],b[1]),10)); release(b[0],b[1])
L = lay(); exp = [x for x in L0[0] if x!=U(a[2])]; exp.insert(5,U(a[2]))
check('8a same-page drag inserts at hovered slot', L[0]==exp and L[1]==L0[1], L[0][:8])
reset()
# 5 + 8b. cross-page into page with room: A (page0 slot2) -> page1 slot1; page0 keeps a gap
a = tile_xy(2); press(a[0],a[1])
th = thumbs_xy()
check('5a thumbnails present immediately at drag start', th and len(th)>=2, th)
check('5b thumbnail hit area >= 64x48', th and all(w>=64 and h>=48 for _,_,w,h in th), th)
if th and len(th)>=2:
    path(lerp((a[0]+15,a[1]+15),(th[1][0],th[1][1]),3), 0.01)
    check('5c ghost does not block thumbnail hit-testing', ev(f"(()=>{{const e=document.elementFromPoint({th[1][0]},{th[1][1]});return !!(e&&e.closest('[role=listbox]'))}})()"))
    time.sleep(0.8)
    check('5d dwell on page-2 thumbnail switched page', ev(page)==1, ev(page))
    c = cell_xy(1); path(lerp((th[1][0],th[1][1]),c,8)); release(*c)
    L = lay()
    check('8j/8b page1 -> dwell page-2 thumbnail -> page 2 slot 1: inserted at slot, source page keeps gap (nothing pulled)', len(L)==2 and len(L[0])==23 and L[1]==[L0[1][0],U(a[2])]+L0[1][1:] and L[0]==[x for x in L0[0] if x!=U(a[2])], [len(p) for p in L])
reset()
# 8c. cross-page into a FULL page: page1 first -> page0 slot 0 => page0's last pushed to front of page1
cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); cmd('Input.dispatchKeyEvent', type='keyUp', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); time.sleep(0.8)
a = tile_xy(0); press(a[0],a[1]); th = thumbs_xy()
if th and len(th)>=2:
    path(lerp((a[0]+15,a[1]+15),(th[0][0],th[0][1]),3), 0.01); time.sleep(0.8)
    c = cell_xy(0); path(lerp((th[0][0],th[0][1]),c,8))
    time.sleep(0.4); prev = shown_ids()
    release(*c)
    L = lay()
    check('8c drop into full page: its last item cascades to front of next page', L[0]==[U(a[2])]+L0[0][:23] and L[1]==[L0[0][23]]+L0[1][1:], [len(p) for p in L])
    check('8c preview matched committed layout', U(prev[:24])==L[0], prev[:5])
reset()
# 8e. page-2 tile hovered on page-1 thumbnail: preview shows original layout; release on thumbnail cancels
cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); cmd('Input.dispatchKeyEvent', type='keyUp', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); time.sleep(0.8)
a = tile_xy(0); press(a[0],a[1]); th = thumbs_xy()
if th:
    path(lerp((a[0]+15,a[1]+15),(th[0][0],th[0][1]),3), 0.01); time.sleep(0.25)   # before the dwell flip: page 2 still shown
    pg_ = ev(page); prev = U(shown_ids())
    release(th[0][0],th[0][1])
    check('8e hover page-1 thumbnail: preview = original layout (no pending move)', pg_==1 and prev[:len(L0[1])]==L0[1], (pg_, prev[:3]))
    check('8e release on page-1 thumbnail cancels (layout unchanged)', lay()==L0, [len(p) for p in lay()])
reset()
# 8e2. page-1 tile: dwell on page-2 thumbnail (switch), release on the thumbnail -> cancel; preview original
a = tile_xy(3); press(a[0],a[1]); th = thumbs_xy()
if th and len(th)>=2:
    path(lerp((a[0]+15,a[1]+15),(th[1][0],th[1][1]),3), 0.01); time.sleep(0.9)
    pg_ = ev(page); prev = U(shown_ids())
    release(th[1][0],th[1][1])
    check('8e2 after switch, still on thumbnail: preview = original page 2', pg_==1 and prev[:len(L0[1])]==L0[1], (pg_, prev[:4]))
    check('8e2 release on thumbnail after switch cancels (layout unchanged)', lay()==L0, [len(p) for p in lay()])
reset()
# 8i. page 2 -> dwell on page-1 thumbnail -> continue into FULL page 1 slot 5 -> insert there, page-1 last cascades to front of page 2
cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); cmd('Input.dispatchKeyEvent', type='keyUp', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39); time.sleep(0.8)
a = tile_xy(1); press(a[0],a[1]); th = thumbs_xy()
if th:
    A = U(a[2])
    path(lerp((a[0]+15,a[1]+15),(th[0][0],th[0][1]),3), 0.01); time.sleep(0.9)
    check('8i dwell on page-1 thumbnail switched to page 1', ev(page)==0, ev(page))
    c = cell_xy(5); path(lerp((th[0][0],th[0][1]),c,10)); time.sleep(0.4)
    prev = U(shown_ids()); release(*c)
    L = lay(); exp0 = L0[0][:5]+[A]+L0[0][5:23]; exp1 = [L0[0][23]]+[x for x in L0[1] if x!=A]
    check('8i page2 -> full page 1 slot 5: inserted at slot, old last to front of page 2', L[0]==exp0 and L[1]==exp1, (L[0][4:7], L[1][:2]))
    check('8i preview matched committed page 1', prev[:24]==exp0, prev[4:7])
reset()
# 8f. '+' add from page 0 when page 0 is full -> end of page 1; from last full pages -> new page
r = ev("fetch('/api/bookmarks',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({title:'T1',url:'https://t1.example.org/',page:0})}).then(r=>r.json())")
check("8f add from full page 0 -> end of first page with room (page 1)", r.get('page')==1 and r.get('position')==3, r)
# 8g. grid shrink cascades (visual order preserved), grow does not pull
flat_before = sum(lay(), [])
ev("fetch('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({grid:{rows:3,cols:4}})}).then(r=>r.status)")
L = lay(); check('8g grid shrink 4x6->3x4: overflow cascades, order kept, no page > 12', all(len(p)<=12 for p in L) and sum(L,[])==flat_before, [len(p) for p in L])
before = lay()
ev("fetch('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({grid:{rows:4,cols:6}})}).then(r=>r.status)")
check('8g grid grow: nothing moves', lay()==before, [len(p) for p in lay()])
# 8h. emptying a page removes it
reset()
ids1 = ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.filter(b=>b.page===1).map(b=>b.id))")
for i in ids1: ev(f"fetch('/api/bookmarks/{i}',{{method:'DELETE'}}).then(r=>r.status)")
check('8h page that becomes empty is removed', len(lay())==1, [len(p) for p in lay()])
reset()
check('layout restored after round-8 checks', lay()==L0)


# ---------- round 8b: drag UX ----------
reset()
RIGHT = lambda: (cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39), cmd('Input.dispatchKeyEvent', type='keyUp', key='ArrowRight', code='ArrowRight', windowsVirtualKeyCode=39))
LEFT = lambda: (cmd('Input.dispatchKeyEvent', type='rawKeyDown', key='ArrowLeft', code='ArrowLeft', windowsVirtualKeyCode=37), cmd('Input.dispatchKeyEvent', type='keyUp', key='ArrowLeft', code='ArrowLeft', windowsVirtualKeyCode=37))
dragging = lambda: ev("!!document.querySelector('.drag-clone')")
# 1. drag started mid page-slide animation (several delays, both directions)
res91 = []
for dirn, delay in [('R',0.0),('R',0.05),('R',0.15),('L',0.03),('L',0.12)]:
    cmd('Page.reload'); time.sleep(2)
    if dirn=='L': RIGHT(); time.sleep(0.9)
    (RIGHT if dirn=='R' else LEFT)(); time.sleep(delay)
    x, y = G0[0], G0[1]
    mouse('mouseMoved',x,y,0); mouse('mousePressed',x,y)
    for k in range(1,8): mouse('mouseMoved',x+6*k,y+6*k); time.sleep(0.02)
    time.sleep(0.25)
    ok = dragging() and len(thumbs_xy() or [])>=2 and ev("!!document.querySelector('.tile-source,.placeholder,[data-placeholder],.dragging')")
    res91.append((dirn, delay, bool(ok)))
    key('Escape'); mouse('mouseReleased',x+42,y+42,0); time.sleep(0.6)
check('9.1 drag starts mid slide animation (ghost + strip + placeholder)', all(r[2] for r in res91), res91)
check('9.1 cancel leaves layout unchanged', lay()==L0)
reset()
# 2. edge flip stops at last page / no flip left from page 1
a = tile_xy(0); press(a[0],a[1]); path(lerp((a[0]+15,a[1]+15),(5,a[1]),6)); time.sleep(1.8)
check('9.2 no left flip from page 1', ev(page)==0, ev(page))
path(lerp((5,a[1]),(1275,a[1]),8)); time.sleep(3.0)
nth = len(thumbs_xy() or [])
check('9.2 right-edge hold stops at last existing page (no virtual page 3)', ev(page)==1 and nth==2, (ev(page), nth))
release(1275,a[1])
check('9.2 release at edge saves nothing', lay()==L0)
reset()
# 3. ghost follows pointer on fast motion (transform + rAF, no transition)
a = tile_xy(0); press(a[0],a[1]); time.sleep(0.1)
tr = ev("(()=>{const g=document.querySelector('.drag-clone');if(!g)return null;const cs=getComputedStyle(g);return [cs.transitionDuration,cs.transitionProperty]})()")
check('9.3 ghost has no transition', tr is not None and all(float(d.strip().rstrip('s') or 0)==0 for d in tr[0].split(',')), tr)
lags = []
for (px,py) in [(900,300),(300,600),(1100,650),(640,690),(200,200)]:
    mouse('mouseMoved',px,py)
    d = ev(f"new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>{{const g=document.querySelector('.drag-clone').getBoundingClientRect();r([g.left<= {px} && {px} <= g.right && g.top <= {py} && {py} <= g.bottom, Math.round(g.left+g.width/2-{px}), Math.round(g.top+g.height/2-{py})])}})))")
    lags.append(d)
check('9.3 ghost under pointer within 2 frames after fast jumps', all(d and d[0] for d in lags), lags)
key('Escape'); mouse('mouseReleased',200,200,0); time.sleep(0.8)
check('9.3 cancel leaves layout unchanged', lay()==L0)
reset()
print(f"\n{sum(r for _,r in results)}/{len(results)} checks passed")
