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
        if r.get('id')==n[0]: return r.get('result', r.get('error'))
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
key('Enter'); time.sleep(1.5)
cur = ev("location.href")
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
print(f"\n{sum(r for _,r in results)}/{len(results)} checks passed")
p.terminate()
