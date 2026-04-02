# -*- coding: utf-8 -*-
"""Generate detailed Embedding Manager flowchart."""
import json
elements = []
seed = 600000
def ns():
    global seed; seed += 1; return seed
def add_text(id,x,y,w,h,text,sz=12,color='#374151',align='center',valign='middle',cid=None):
    elements.append({'type':'text','id':id,'x':x,'y':y,'width':w,'height':h,'text':text,'originalText':text,'fontSize':sz,'fontFamily':3,'textAlign':align,'verticalAlign':valign,'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':1,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'containerId':cid,'lineHeight':1.25})
def add_rect(id,x,y,w,h,fill,stroke,sw=2):
    elements.append({'type':'rectangle','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':sw,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False,'roundness':{'type':3}})
    return (x,y,w,h)
def add_diamond(id,x,y,w,h,fill,stroke):
    elements.append({'type':'diamond','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x,y,w,h)
def add_ellipse(id,x,y,w,h,fill,stroke):
    elements.append({'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x,y,w,h)
def add_arrow(id,x1,y1,x2,y2,color,sw=2,style='solid'):
    dx=x2-x1;dy=y2-y1
    elements.append({'type':'arrow','id':id,'x':x1,'y':y1,'width':abs(dx),'height':abs(dy),'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'points':[[0,0],[dx,dy]],'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})
def add_arrow_path(id,pts,color,sw=2,style='solid'):
    x0,y0=pts[0];rel=[[px-x0,py-y0] for px,py in pts];w=max(abs(p[0]) for p in rel) if rel else 0;h=max(abs(p[1]) for p in rel) if rel else 0
    elements.append({'type':'arrow','id':id,'x':x0,'y':y0,'width':w,'height':h,'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'points':rel,'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})
def bot(b): return (b[0]+b[2]/2, b[1]+b[3])
def top(b): return (b[0]+b[2]/2, b[1])
def lft(b): return (b[0], b[1]+b[3]/2)
def rgt(b): return (b[0]+b[2], b[1]+b[3]/2)

C_BLUE='#3b82f6';S_BLUE='#1e3a5f';C_GREEN='#a7f3d0';S_GREEN='#047857';C_YELLOW='#fef3c7';S_YELLOW='#b45309';C_PURPLE='#ddd6fe';S_PURPLE='#6d28d9';C_RED='#fee2e2';S_RED='#dc2626';C_GRAY='#f1f5f9';S_GRAY='#64748b'
GAP=55;W=240;H=56;DW=180;DH=80;NW=280;cx=400;nx=cx+W/2+40

add_text('title',80,15,640,28,'Embedding Manager — 多Provider调度与熔断',22,'#1e40af')
add_text('sub',80,48,640,15,'EmbedStrings() → LoadBalance → CircuitBreaker → Retry/Failover → HealthCheck',9,S_GRAY)
y=80

# ── START ──
n0=add_ellipse('start',cx-70,y,140,40,'#fed7aa','#c2410c')
add_text('start_t',cx-55,y+8,110,24,'EmbedStrings()',12,'#c2410c','center','middle','start')
add_text('start_f',cx-70,y+44,140,12,'embedding_manager.go',9,S_GRAY)
add_arrow('a01',*bot(n0),cx,y+40+GAP,S_PURPLE)
y+=40+GAP

# ── Get Available Providers ──
n1=add_rect('gap',cx-W/2,y,W,H,C_PURPLE,S_PURPLE)
add_text('gap_t',cx-W/2+5,y+4,W-10,H-8,'getAvailableProviders()\nPriority排序: 数值小=高优先',10,S_PURPLE,'center','middle','gap')
add_rect('gap_n',nx,y-6,NW,68,C_GRAY,S_GRAY,1)
add_text('gap_nt',nx+8,y,NW-16,56,'说明: Provider排序策略\n• Priority: 按优先级升序\n• RoundRobin: 原子计数器轮转\n• Weighted: 加权随机选择\n• Random: 纯随机\n过滤掉 Enabled=false 的Provider',9,'#374151','left','top','gap_n')
add_arrow('a02',*bot(n1),cx,y+H+GAP,S_PURPLE)
y+=H+GAP

# ── Retry Loop ──
add_rect('loop_bg',cx-W/2-20,y-10,W+40,DH+H+GAP+H+GAP+20,'#f8fafc','#94a3b8',1)
add_text('loop_l',cx-W/2-15,y-6,180,14,'for attempt=0..MaxRetries(3)',9,S_GRAY,'left')

# ── Select Provider ──
n2=add_rect('sel',cx-W/2,y+10,W,H,C_PURPLE,S_PURPLE)
add_text('sel_t',cx-W/2+5,y+14,W-10,H-8,'selectProvider(attempt)\nPriority: attempt=索引降级',10,S_PURPLE,'center','middle','sel')
add_rect('sel_n',nx,y+4,NW,52,C_GRAY,S_GRAY,1)
add_text('sel_nt',nx+8,y+10,NW-16,40,'说明: 故障转移选择\n• attempt=0 → 最高优先级Provider\n• attempt=1 → 次优先级(备用)\n• 所有试完后回到首位重试',9,'#374151','left','top','sel_n')
add_arrow('a03',*bot(n2),cx,y+10+H+GAP,S_PURPLE)

# ── Circuit Breaker Check ──
cb_y=y+10+H+GAP
d1=add_diamond('d_cb',cx-DW/2,cb_y,DW,DH,C_RED,S_RED)
add_text('d_cb_t',cx-36,cb_y+DH/2-8,72,16,'canUse\nProvider?',9,S_RED,'center','middle','d_cb')

# Open → skip
add_arrow_path('a_cb_no',[rgt(d1),(nx+10,cb_y+DH/2),(nx+10,y+10)],S_RED,1,'dashed')
add_text('a_cb_nol',nx-30,cb_y+DH/2-16,60,12,'Open→skip',8,S_RED)

add_rect('cb_n',nx,cb_y-6,NW,80,C_GRAY,S_GRAY,1)
add_text('cb_nt',nx+8,cb_y,NW-16,68,'说明: 熔断器三态状态机\n• Closed → 正常放行\n• Open → 快速拒绝(跳到下一个)\n• HalfOpen → 冷却期后限流探测\n  (最多CircuitHalfOpenMax=3个请求)\n连续失败≥5次 → Closed→Open',9,'#374151','left','top','cb_n')

# Pass → down to call
add_arrow('a_cb_y',cx,cb_y+DH,cx,cb_y+DH+GAP,S_GREEN)
add_text('a_cb_yl',cx+8,cb_y+DH+6,30,12,'Pass',9,S_GREEN)

# ── Call Provider ──
call_y=cb_y+DH+GAP
n3=add_rect('call',cx-W/2,call_y,W,H,C_BLUE,S_BLUE)
add_text('call_t',cx-W/2+5,call_y+4,W-10,H-8,'embedWithProvider()\nprovider.embedder.EmbedStrings()',10,'#ffffff','center','middle','call')
add_arrow('a04',*bot(n3),cx,call_y+H+GAP,S_GREEN)

# ── Success/Fail Decision ──
sf_y=call_y+H+GAP
d2=add_diamond('d_sf',cx-DW/2,sf_y,DW,DH,C_YELLOW,S_YELLOW)
add_text('d_sf_t',cx-30,sf_y+DH/2-8,60,16,'Success?',10,S_YELLOW,'center','middle','d_sf')

# Success → left exit
add_arrow_path('a_sf_y',[lft(d2),(40,sf_y+DH/2),(40,sf_y+DH+100)],S_GREEN)
add_text('a_sf_yl',48,sf_y+DH/2-16,50,12,'Success',9,S_GREEN)

n_ok=add_rect('ok',10,sf_y+DH+100,180,H,C_GREEN,S_GREEN,3)
add_text('ok_t',15,sf_y+DH+104,170,H-8,'Return vectors\n清零consecutiveFails',10,S_GREEN,'center','middle','ok')
add_rect('ok_n',10,sf_y+DH+100+H+8,180,44,C_GRAY,S_GRAY,1)
add_text('ok_nt',18,sf_y+DH+100+H+14,164,32,'HalfOpen探测成功时\n→ 恢复Closed状态\nconsecutiveFails = 0',9,'#374151','left','top','ok_n')

# Fail → right: update breaker + retry
add_arrow_path('a_sf_n',[rgt(d2),(nx+30,sf_y+DH/2)],S_RED)
add_text('a_sf_nl',nx-30,sf_y+DH/2-16,30,12,'Fail',9,S_RED)

n_fail=add_rect('fail',nx,sf_y+DH/2-H/2,W,H,C_RED,S_RED)
add_text('fail_t',nx+5,sf_y+DH/2-H/2+4,W-10,H-8,'consecutiveFails++\n≥5次 → Open 熔断',10,S_RED,'center','middle','fail')

# Retry delay
n_delay=add_rect('delay',nx,sf_y+DH/2+H/2+GAP,W,H,C_YELLOW,S_YELLOW)
add_text('delay_t',nx+5,sf_y+DH/2+H/2+GAP+4,W-10,H-8,'Exponential Backoff\ndelay×2^n + random jitter',10,S_YELLOW,'center','middle','delay')
add_arrow('a_f1',*bot(n_fail),*top(n_delay),S_RED)

add_rect('delay_n',nx+W+20,sf_y+DH/2+H/2+GAP-6,NW-40,56,C_GRAY,S_GRAY,1)
add_text('delay_nt',nx+W+28,sf_y+DH/2+H/2+GAP,NW-56,44,'说明: 指数退避+抖动\n• 1s → 2s → 4s (×2倍增)\n• 上限30s\n• jitter=delay/4 防惊群',9,'#374151','left','top','delay_n')

# Loop back arrow
add_arrow_path('a_retry',[bot(n_delay),(nx+W/2,sf_y+DH/2+H/2+GAP+H+30),(nx+W+50,sf_y+DH/2+H/2+GAP+H+30),(nx+W+50,y+10+H/2),(cx+W/2,y+10+H/2)],S_RED,1,'dashed')
add_text('a_retry_l',nx+W+55,y+10+H/2+4,80,12,'next attempt',8,S_RED,'left')

# ── HEALTH CHECK (separate section on the right far side) ──
hc_x = 50
hc_y = sf_y + DH + 200 + 60

add_text('hc_title',hc_x,hc_y,300,20,'后台健康检查协程 (healthCheckLoop)',14,'#1e40af','left')
add_text('hc_sub',hc_x,hc_y+22,400,14,'每60s对非Closed的Provider发起轻量探测请求',9,S_GRAY,'left')

hc_y2 = hc_y + 46
n_hc1=add_rect('hc1',hc_x,hc_y2,200,H,C_YELLOW,S_YELLOW)
add_text('hc1_t',hc_x+5,hc_y2+4,190,H-8,'Tick every 60s\n遍历 providers',10,S_YELLOW,'center','middle','hc1')
add_arrow('ahc1',*rgt(n_hc1),hc_x+200+GAP,hc_y2+H/2,S_YELLOW)

d_hc=add_diamond('d_hc',hc_x+200+GAP,hc_y2-12,DW,DH,C_RED,S_RED)
add_text('d_hc_t',hc_x+200+GAP+DW/2-30,hc_y2+DH/2-20,60,16,'State ==\nClosed?',9,S_RED,'center','middle','d_hc')

# Closed → skip
add_text('d_hc_yl',hc_x+200+GAP+DW/2+8,hc_y2-20,25,12,'Yes',9,S_GREEN)
add_arrow('ahc_skip',hc_x+200+GAP+DW/2,hc_y2-12,hc_x+200+GAP+DW/2,hc_y2-40,S_GREEN)
add_text('ahc_skip_l',hc_x+200+GAP+DW/2+8,hc_y2-46,40,12,'Skip',9,S_GREEN,'left')

# Not Closed → probe
add_arrow('ahc2',hc_x+200+GAP+DW,hc_y2+DH/2-12,hc_x+200+GAP+DW+60,hc_y2+DH/2-12,S_RED)
add_text('ahc2_l',hc_x+200+GAP+DW+5,hc_y2+DH/2-28,20,12,'No',9,S_RED)

n_hc2=add_rect('hc2',hc_x+200+GAP+DW+60,hc_y2-12,200,H,C_BLUE,S_BLUE)
add_text('hc2_t',hc_x+200+GAP+DW+65,hc_y2-8,190,H-8,'Probe: Embed(" ")\n单空格最小API调用',10,'#ffffff','center','middle','hc2')

add_arrow('ahc3',*bot(n_hc2),hc_x+200+GAP+DW+160,hc_y2+H+30,S_GREEN)

n_hc3=add_rect('hc3',hc_x+200+GAP+DW+60,hc_y2+H+30,200,H,C_GREEN,S_GREEN)
add_text('hc3_t',hc_x+200+GAP+DW+65,hc_y2+H+34,190,H-8,'成功: Open→Closed\n直接恢复(跳过HalfOpen)',10,S_GREEN,'center','middle','hc3')

add_rect('hc_n',hc_x+200+GAP+DW+60+210,hc_y2-12,NW-40,80,C_GRAY,S_GRAY,1)
add_text('hc_nt',hc_x+200+GAP+DW+68+210,hc_y2-6,NW-56,68,'说明: 主动健康探测\n• 只探测非Closed的Provider\n• Closed态零API调用开销\n• 探测成功直接恢复Closed\n• 比被动等请求失败更快发现恢复\n• ctx.Done()时协程优雅退出',9,'#374151','left','top','hc_n')

# ── LEGEND ──
leg_y = hc_y2+H+30+H+40
add_rect('leg',hc_x,leg_y,200,110,C_GRAY,S_GRAY,1)
add_text('legt',hc_x+10,leg_y+5,180,16,'图例',12,'#374151','left')
for i,(c,lb) in enumerate([
    (C_PURPLE,'调度/选择逻辑'),(C_BLUE,'API调用/网络'),
    (C_RED,'熔断/错误处理'),(C_GREEN,'成功/恢复'),
    (C_YELLOW,'等待/重试/探测'),
]):
    add_rect(f'lg{i}',hc_x+10,leg_y+26+i*16,14,10,c,'#374151',1)
    add_text(f'lg{i}t',hc_x+30,leg_y+25+i*16,160,12,lb,9,'#374151','left')

doc={'type':'excalidraw','version':2,'source':'https://excalidraw.com','elements':elements,'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}
with open('module-embedding.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc,f,ensure_ascii=False,indent=2)
print(f'OK! {len(elements)} elements → module-embedding.excalidraw')
