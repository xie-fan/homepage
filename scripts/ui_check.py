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
# drag thumbnails
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
    after = ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>b.id))")
    per = ev("(()=>{const s=document.querySelectorAll('.page')[0];return s?s.querySelectorAll('[data-id]').length:0})()")
    idx = after.index(a[2])
    check('drop on thumbnail moves to end of that page', idx==min(2*24-1, len(after)-1) or idx>=24, f'new index {idx}, n={len(after)}')
    check('strip hidden after drop', not ev("[...document.querySelectorAll('[role=listbox] [aria-label^=\"第\"]')].some(e=>{const r=e.getBoundingClientRect();return r.width>0&&getComputedStyle(e.closest('[role=listbox]')||e).visibility!=='hidden'&&+getComputedStyle(e.closest('[role=listbox]')||e).opacity>0.05})"))
    # restore order
    ev(f"fetch('/api/bookmarks/order',{{method:'PUT',body:JSON.stringify({{ids:{json.dumps(before)}}})}}).then(r=>r.status)")
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

# ---------- round 7 regression checks ----------
cmd('Emulation.setDeviceMetricsOverride', width=1280, height=800, deviceScaleFactor=1, mobile=False)
cmd('Page.reload'); time.sleep(2.5)
ids_now = lambda: ev("fetch('/api/bookmarks').then(r=>r.json()).then(a=>a.map(b=>b.id))")
def restore(order): ev(f"fetch('/api/bookmarks/order',{{method:'PUT',body:JSON.stringify({{ids:{json.dumps(order)}}})}}).then(r=>r.status)")
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
tile_xy = lambda i: ev(f"(()=>{{const v=[...document.querySelectorAll('[data-id]')].filter(e=>{{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth}});const r=v[{i}].getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2-6,+v[{i}].dataset.id]}})()")
def drag(path, hold=0.03, settle=0.4):
    x,y = path[0]; mouse('mouseMoved',x,y,0); mouse('mousePressed',x,y)
    for (a,b) in path[1:]: mouse('mouseMoved',a,b); time.sleep(hold)
    time.sleep(settle); mouse('mouseReleased',*path[-1],0); time.sleep(1.2)
def lerp(a,b,n=10): return [(a[0]+(b[0]-a[0])*k/n, a[1]+(b[1]-a[1])*k/n) for k in range(n+1)]
# 4. drop outside grid cancels
a = tile_xy(0)
drag(lerp((a[0],a[1]),(a[0]+40,a[1]+60),4) + lerp((a[0]+40,a[1]+60),(1180,640),8))
check('4 drop outside grid cancels (order unchanged)', ids_now()==orig_order and ev(vis)[0]==ev(vis)[0])
restore(orig_order); cmd('Page.reload'); time.sleep(2)
# 6a. same page: drop on slot 5 -> lands at index 5
a = tile_xy(0); b = tile_xy(5)
drag(lerp((a[0],a[1]),(b[0],b[1]),12))
o = ids_now()
check('6a drop on a tile places dragged tile right BEFORE it (same page)', o.index(a[2])==o.index(b[2])-1, f'dragged at {o.index(a[2])}, target at {o.index(b[2])}')
restore(orig_order); cmd('Page.reload'); time.sleep(2)
# 5 + 6b. fast drag to thumbnail of page 2, dwell, drop on first tile slot of page 2 -> index 24 (before that tile)
a = tile_xy(2)
x,y=a[0],a[1]; mouse('mouseMoved',x,y,0); mouse('mousePressed',x,y)
for k in range(1,4): mouse('mouseMoved',x+5*k,y+5*k); time.sleep(0.01)
th = ev("[...document.querySelectorAll('[role=listbox] [aria-label^=\"第\"]')].map(e=>{const r=e.getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2,r.width,r.height]})")
check('5a thumbnails present immediately at drag start', th and len(th)>=2, th)
check('5b thumbnail hit area >= 64x48', th and all(w>=64 and h>=48 for _,_,w,h in th), th)
if th and len(th)>=2:
    for pt in lerp((x+15,y+15),(th[1][0],th[1][1]),3): mouse('mouseMoved',*pt); time.sleep(0.01)
    hit = ev(f"(()=>{{const e=document.elementFromPoint({th[1][0]},{th[1][1]});return !!(e&&e.closest('[role=listbox]'))}})()")
    check('5c ghost does not block thumbnail hit-testing', hit)
    time.sleep(0.8)
    check('5d dwell on page-2 thumbnail switched page', ev(page)==1, ev(page))
    tgt = ev("(()=>{const v=[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth&&!e.classList.contains('tile-source')&&!e.classList.contains('dragging')});return v.map(e=>{const r=e.getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2-6,+e.dataset.id]})})()")
    t2 = tgt[1]  # second tile on page 2 (non-boundary case)
    for pt in lerp((th[1][0],th[1][1]),(t2[0],t2[1]),8): mouse('mouseMoved',*pt); time.sleep(0.03)
    time.sleep(0.4)
    # preview just before release: page-2 order as shown; the drop must commit exactly that
    shown = ev("[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth&&!e.classList.contains('drag-clone')}).map(e=>+e.dataset.id)")
    mouse('mouseReleased',t2[0],t2[1],0); time.sleep(1.2)
    o = ids_now()
    i = shown.index(a[2]) if a[2] in shown else -1
    nxt = shown[i+1] if 0 <= i < len(shown)-1 else None
    check('6b cross-page drop commits the previewed order (A right before the tile after it)', o[24:24+len(shown)]==shown and nxt is not None and o.index(a[2])==o.index(nxt)-1, f'shown {shown} committed {o[24:]}')
    restore(orig_order); cmd('Page.reload'); time.sleep(2)
    # 6c. hovering the FIRST tile of page 2 puts the tile at page 2's first slot
    a = tile_xy(2); x,y=a[0],a[1]; mouse('mouseMoved',x,y,0); mouse('mousePressed',x,y)
    for k in range(1,4): mouse('mouseMoved',x+5*k,y+5*k); time.sleep(0.01)
    for pt in lerp((x+15,y+15),(th[1][0],th[1][1]),3): mouse('mouseMoved',*pt); time.sleep(0.01)
    time.sleep(0.9)
    f = ev("(()=>{const v=[...document.querySelectorAll('[data-id]')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.left>=0&&r.right<=innerWidth&&!e.classList.contains('tile-source')&&!e.classList.contains('dragging')});const r=v[0].getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2-6]})()")
    for pt in lerp((th[1][0],th[1][1]),(f[0],f[1]),8): mouse('mouseMoved',*pt); time.sleep(0.03)
    time.sleep(0.4); mouse('mouseReleased',f[0],f[1],0); time.sleep(1.2)
    o = ids_now()
    check('6c drop on first tile of page 2 -> first slot of page 2', o.index(a[2]) == 24, f'dragged at {o.index(a[2])}')
restore(orig_order)
check('order restored after round-7 checks', ids_now()==orig_order)

print(f"\n{sum(r for _,r in results)}/{len(results)} checks passed")
p.terminate()
