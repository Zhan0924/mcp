# -*- coding: utf-8 -*-
"""Generate flowchart with proper spacing to avoid overlaps."""
import json

elements = []
seed = 400000
def ns():
    global seed; seed += 1; return seed

def add_text(id,x,y,w,h,text,sz=12,color='#374151',align='center',valign='middle',cid=None):
    elements.append({'type':'text','id':id,'x':x,'y':y,'width':w,'height':h,
        'text':text,'originalText':text,'fontSize':sz,'fontFamily':3,
        'textAlign':align,'verticalAlign':valign,'strokeColor':color,
        'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':1,
        'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'containerId':cid,'lineHeight':1.25})

def add_rect(id,x,y,w,h,fill,stroke,sw=2):
    elements.append({'type':'rectangle','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':[],'link':None,'locked':False,
        'roundness':{'type':3}})
    return (x,y,w,h)

def add_diamond(id,x,y,w,h,fill,stroke):
    elements.append({'type':'diamond','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid',
        'strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x,y,w,h)

def add_ellipse(id,x,y,w,h,fill,stroke):
    elements.append({'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid',
        'strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x,y,w,h)

def add_arrow(id, x1, y1, x2, y2, color, sw=2, style='solid'):
    dx=x2-x1; dy=y2-y1
    elements.append({'type':'arrow','id':id,'x':x1,'y':y1,
        'width':abs(dx),'height':abs(dy),
        'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'points':[[0,0],[dx,dy]],
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})

def add_arrow_path(id, pts_abs, color, sw=2, style='solid'):
    x0,y0 = pts_abs[0]
    rel = [[px-x0, py-y0] for px,py in pts_abs]
    w = max(abs(p[0]) for p in rel) if rel else 0
    h = max(abs(p[1]) for p in rel) if rel else 0
    elements.append({'type':'arrow','id':id,'x':x0,'y':y0,'width':w,'height':h,
        'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'points':rel,
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})

def bot(b): return (b[0]+b[2]/2, b[1]+b[3])
def top(b): return (b[0]+b[2]/2, b[1])
def lft(b): return (b[0], b[1]+b[3]/2)
def rgt(b): return (b[0]+b[2], b[1]+b[3]/2)

# ── Layout constants ──
GAP = 60       # vertical gap (big)
W   = 210      # box width
H   = 60       # box height
DW  = 170      # diamond width
DH  = 90       # diamond height

cx  = 520      # center column X
lx  = 60       # left column X  (search flow boxes start here)
rx  = 960      # right column X (index flow boxes start here)
gx  = 1400     # graph column X

# ==============================================================
# TITLE
# ==============================================================
add_text('title', 250, 10, 700, 32, 'MCP RAG Server \u2014 \u6838\u5fc3\u6d41\u7a0b\u56fe', 24, '#1e40af')
add_text('sub', 250, 44, 700, 16, 'Client \u2192 HTTP \u2192 MCP Server \u2192 Tool Dispatch \u2192 Search / Index / Graph \u2192 Response', 10, '#64748b')

# ==============================================================
# MAIN TRUNK
# ==============================================================
y = 80
n0 = add_ellipse('start', cx-65, y, 130, 45, '#fed7aa', '#c2410c')
add_text('start_t', cx-55, y+10, 110, 25, 'MCP Client', 13, '#c2410c','center','middle','start')

add_arrow('a01', *bot(n0), cx, y+45+GAP, '#c2410c')
add_text('a01l', cx+8, y+50, 110, 14, 'HTTP POST /mcp', 9, '#64748b','left')

y += 45+GAP
n1 = add_rect('http', cx-W/2, y, W, H, '#3b82f6', '#1e3a5f')
add_text('http_t', cx-W/2+10, y+6, W-20, H-12, 'Streamable HTTP\nserver.go', 11, '#ffffff','center','middle','http')

add_arrow('a02', *bot(n1), *top((cx-W/2, y+H+GAP, W, H)), '#1e3a5f')

y += H+GAP
n2 = add_rect('srv', cx-W/2, y, W, H, '#3b82f6', '#1e3a5f', 3)
add_text('srv_t', cx-W/2+10, y+6, W-20, H-12, 'MCP Server\nJSON-RPC dispatch', 11, '#ffffff','center','middle','srv')

add_arrow('a03', *bot(n2), *top((cx-W/2, y+H+GAP, W, H)), '#1e3a5f')

y += H+GAP
n3 = add_rect('reg', cx-W/2, y, W, H, '#60a5fa', '#1e3a5f')
add_text('reg_t', cx-W/2+10, y+6, W-20, H-12, 'Registry\nRoute to Tool Handler', 11, '#ffffff','center','middle','reg')

add_arrow('a04', *bot(n3), cx, y+H+GAP, '#1e3a5f')

y += H+GAP
nd1 = add_diamond('d1', cx-DW/2, y, DW, DH, '#fef3c7', '#b45309')
add_text('d1_t', cx-40, y+DH/2-8, 80, 16, 'Tool Type?', 11, '#b45309','center','middle','d1')

dy_mid = y + DH/2  # y of diamond horizontal midline

# ==============================================================
# LEFT BRANCH: Search (lx=80, boxes width W=190, so x: 80..270)
# ==============================================================
add_text('fl_s', lx, dy_mid-36, W, 16, '\u2460 \u68c0\u7d22\u6d41\u7a0b (Search)', 11, '#047857')

# Arrow: diamond left -> down -> top of QV
s_y0 = dy_mid + 30  # first box top
add_arrow_path('aL0', [(cx-DW/2, dy_mid), (lx+W/2, dy_mid), (lx+W/2, s_y0)], '#047857')
add_text('aL0l', lx+W/2+8, dy_mid-16, 60, 14, 'search', 10, '#047857','left')

n_qv = add_rect('qv', lx, s_y0, W, H, '#a7f3d0', '#047857')
add_text('qv_t', lx+10, s_y0+6, W-20, H-12, 'Query Validation\nisValidQuery()', 11, '#047857','center','middle','qv')

add_arrow('aS1', *bot(n_qv), lx+W/2, s_y0+H+GAP, '#047857')

# Diamond: HyDE?
s_y1 = s_y0+H+GAP
nd2 = add_diamond('d2', lx+W/2-DW/2, s_y1, DW, DH, '#fef3c7', '#b45309')
add_text('d2_t', lx+W/2-28, s_y1+DH/2-8, 56, 16, 'HyDE?', 10, '#b45309','center','middle','d2')

# Yes -> down
add_arrow('aS2y', lx+W/2, s_y1+DH, lx+W/2, s_y1+DH+GAP, '#047857')
add_text('aS2yl', lx+W/2+8, s_y1+DH+6, 25, 12, 'Yes', 9, '#047857','left')

s_y2 = s_y1+DH+GAP
n_hyde = add_rect('hyde', lx, s_y2, W, H, '#fef3c7', '#b45309')
add_text('hyde_t', lx+10, s_y2+6, W-20, H-12, 'HyDE / MultiQuery\nTransform()', 11, '#b45309','center','middle','hyde')

# No -> bypass (left side detour)
byp_x = lx - 40
add_arrow_path('aS2n', [(lx+W/2-DW/2, s_y1+DH/2), (byp_x, s_y1+DH/2), (byp_x, s_y2+H+GAP+H/2), (lx, s_y2+H+GAP+H/2)], '#64748b', 1, 'dashed')
add_text('aS2nl', byp_x-20, s_y1+DH/2-16, 20, 12, 'No', 9, '#dc2626')

add_arrow('aS3', *bot(n_hyde), lx+W/2, s_y2+H+GAP, '#047857')

# Embed Query
s_y3 = s_y2+H+GAP
n_eq = add_rect('eq', lx, s_y3, W, H, '#ddd6fe', '#6d28d9')
add_text('eq_t', lx+10, s_y3+6, W-20, H-12, 'Embed Query\nEmbedStrings()', 11, '#6d28d9','center','middle','eq')

add_arrow('aS4', *bot(n_eq), lx+W/2, s_y3+H+GAP, '#047857')

# Diamond: Hybrid?
s_y4 = s_y3+H+GAP
nd3 = add_diamond('d3', lx+W/2-DW/2, s_y4, DW, DH, '#93c5fd', '#1e3a5f')
add_text('d3_t', lx+W/2-28, s_y4+DH/2-8, 56, 16, 'Hybrid?', 10, '#1e3a5f','center','middle','d3')

# Two sub-branches for Vector and Keyword
vbw = 170  # sub-branch box width
vx = lx - 60  # vector search box x
kx = lx + W + 20  # keyword search box x
sb_y = s_y4 + DH + GAP

# Left: Vector only
add_arrow_path('aH1', [(lx+W/2-DW/2, s_y4+DH/2), (vx+vbw/2, s_y4+DH/2), (vx+vbw/2, sb_y)], '#1e3a5f')
add_text('aH1l', vx+vbw/2-30, s_y4+DH/2-18, 50, 14, 'Vector', 9, '#1e3a5f')

n_vs = add_rect('vs', vx, sb_y, vbw, H, '#3b82f6', '#1e3a5f')
add_text('vs_t', vx+10, sb_y+6, vbw-20, H-12, 'Vector Search\nFT.SEARCH KNN', 10, '#ffffff','center','middle','vs')

# Right: Keyword
add_arrow_path('aH2', [(lx+W/2+DW/2, s_y4+DH/2), (kx+vbw/2, s_y4+DH/2), (kx+vbw/2, sb_y)], '#1e3a5f')
add_text('aH2l', kx+vbw/2-30, s_y4+DH/2-18, 60, 14, 'Keyword', 9, '#1e3a5f')

n_ks = add_rect('ks', kx, sb_y, vbw, H, '#93c5fd', '#1e3a5f')
add_text('ks_t', kx+10, sb_y+6, vbw-20, H-12, 'Keyword Search\nBM25 text', 10, '#1e3a5f','center','middle','ks')

# Merge arrows -> RRF
rrf_y = sb_y + H + GAP
add_arrow_path('aM1', [bot(n_vs), (vx+vbw/2, rrf_y-10), (lx+W/2, rrf_y-10), (lx+W/2, rrf_y)], '#1e3a5f')
add_arrow_path('aM2', [bot(n_ks), (kx+vbw/2, rrf_y-10), (lx+W/2, rrf_y-10), (lx+W/2, rrf_y)], '#1e3a5f')

n_rrf = add_rect('rrf', lx, rrf_y, W, H, '#93c5fd', '#1e3a5f')
add_text('rrf_t', lx+10, rrf_y+6, W-20, H-12, 'RRF Merge\nmergeByRRF()', 11, '#1e3a5f','center','middle','rrf')

add_arrow('aS5', *bot(n_rrf), lx+W/2, rrf_y+H+GAP, '#047857')

# Diamond: Rerank?
s_y6 = rrf_y+H+GAP
nd4 = add_diamond('d4', lx+W/2-DW/2, s_y6, DW, DH, '#ddd6fe', '#6d28d9')
add_text('d4_t', lx+W/2-28, s_y6+DH/2-8, 56, 16, 'Rerank?', 10, '#6d28d9','center','middle','d4')

# Yes
add_arrow('aS6y', lx+W/2, s_y6+DH, lx+W/2, s_y6+DH+GAP, '#047857')
add_text('aS6yl', lx+W/2+8, s_y6+DH+6, 25, 12, 'Yes', 9, '#047857','left')

s_y7 = s_y6+DH+GAP
n_rrk = add_rect('rrk', lx, s_y7, W, H, '#ddd6fe', '#6d28d9')
add_text('rrk_t', lx+10, s_y7+6, W-20, H-12, 'Reranker\nDashScope / Qwen3', 11, '#6d28d9','center','middle','rrk')

# No bypass
add_arrow_path('aS6n', [(lx+W/2-DW/2, s_y6+DH/2), (byp_x, s_y6+DH/2), (byp_x, s_y7+H+GAP+H/2), (lx, s_y7+H+GAP+H/2)], '#64748b', 1, 'dashed')
add_text('aS6nl', byp_x-20, s_y6+DH/2-16, 20, 12, 'No', 9, '#dc2626')

add_arrow('aS7', *bot(n_rrk), lx+W/2, s_y7+H+GAP, '#047857')

# Compress
s_y8 = s_y7+H+GAP
n_cc = add_rect('cc', lx, s_y8, W, H, '#fef3c7', '#b45309')
add_text('cc_t', lx+10, s_y8+6, W-20, H-12, 'Context Compress\nCompress()', 11, '#b45309','center','middle','cc')

add_arrow('aS8', *bot(n_cc), lx+W/2, s_y8+H+GAP, '#047857')

# Return
s_y9 = s_y8+H+GAP
n_rr = add_rect('rr', lx, s_y9, W, H, '#a7f3d0', '#047857')
add_text('rr_t', lx+10, s_y9+6, W-20, H-12, 'Return Results\n[]RetrievalResult', 11, '#047857','center','middle','rr')

# ==============================================================
# RIGHT BRANCH: Index (rx=820)
# ==============================================================
add_text('fl_i', rx, dy_mid-36, W, 16, '\u2461 \u7d22\u5f15\u6d41\u7a0b (Index)', 11, '#dc2626')

i_y0 = dy_mid + 30
add_arrow_path('aR0', [(cx+DW/2, dy_mid), (rx+W/2, dy_mid), (rx+W/2, i_y0)], '#dc2626')
add_text('aR0l', rx+W/2+8, dy_mid-16, 80, 14, 'index', 10, '#dc2626','left')

n_pd = add_rect('pd', rx, i_y0, W, H, '#93c5fd', '#1e3a5f')
add_text('pd_t', rx+10, i_y0+6, W-20, H-12, 'Parse Document\nMD / HTML / PDF / DOCX', 10, '#1e3a5f','center','middle','pd')

add_arrow('aI1', *bot(n_pd), rx+W/2, i_y0+H+GAP, '#dc2626')

i_y1 = i_y0+H+GAP
n_ct = add_rect('ct', rx, i_y1, W, H, '#93c5fd', '#1e3a5f')
add_text('ct_t', rx+10, i_y1+6, W-20, H-12, 'Chunk Text\nStructure / Semantic / Code', 10, '#1e3a5f','center','middle','ct')

add_arrow('aI2', *bot(n_ct), rx+W/2, i_y1+H+GAP, '#dc2626')

# Async?
i_y2 = i_y1+H+GAP
nd5 = add_diamond('d5', rx+W/2-DW/2, i_y2, DW, DH, '#fee2e2', '#dc2626')
add_text('d5_t', rx+W/2-25, i_y2+DH/2-8, 50, 16, 'Async?', 10, '#dc2626','center','middle','d5')

# Yes -> side
add_arrow_path('aI3y', [(rx+W/2+DW/2, i_y2+DH/2), (rx+W+40, i_y2+DH/2)], '#dc2626')
add_text('aI3yl', rx+W/2+DW/2+5, i_y2+DH/2-18, 25, 12, 'Yes', 9, '#dc2626')

n_wq = add_rect('wq', rx+W+40, i_y2+DH/2-22, 140, 44, '#fee2e2', '#dc2626', 1)
add_text('wq_t', rx+W+50, i_y2+DH/2-16, 120, 32, 'Redis Queue\nWorker Pool', 9, '#dc2626','center','middle','wq')

# No -> down
add_arrow('aI3n', rx+W/2, i_y2+DH, rx+W/2, i_y2+DH+GAP, '#dc2626')
add_text('aI3nl', rx+W/2+8, i_y2+DH+6, 20, 12, 'No', 9, '#64748b','left')

# Embed
i_y3 = i_y2+DH+GAP
n_ec = add_rect('ec', rx, i_y3, W, H, '#ddd6fe', '#6d28d9')
add_text('ec_t', rx+10, i_y3+6, W-20, H-12, 'Embed Chunks\nEmbedStrings()', 11, '#6d28d9','center','middle','ec')

add_arrow('aI4', *bot(n_ec), rx+W/2, i_y3+H+GAP, '#dc2626')

# Cache?
i_y4 = i_y3+H+GAP
nd6 = add_diamond('d6', rx+W/2-DW/2, i_y4, DW, DH, '#a7f3d0', '#047857')
add_text('d6_t', rx+W/2-25, i_y4+DH/2-8, 50, 16, 'Cache?', 10, '#047857','center','middle','d6')

# Hit -> side
add_arrow_path('aI5h', [(rx+W/2+DW/2, i_y4+DH/2), (rx+W+40, i_y4+DH/2)], '#047857')
add_text('aI5hl', rx+W/2+DW/2+5, i_y4+DH/2-18, 40, 12, 'Hit', 9, '#047857')

n_ch = add_rect('ch', rx+W+40, i_y4+DH/2-18, 120, 36, '#a7f3d0', '#047857', 1)
add_text('ch_t', rx+W+48, i_y4+DH/2-12, 104, 24, 'Cache Return\nLRU / Redis', 8, '#047857','center','middle','ch')

# Miss -> down
add_arrow('aI5m', rx+W/2, i_y4+DH, rx+W/2, i_y4+DH+GAP, '#dc2626')
add_text('aI5ml', rx+W/2+8, i_y4+DH+6, 30, 12, 'Miss', 9, '#dc2626','left')

# Embedding API
i_y5 = i_y4+DH+GAP
n_ea = add_rect('ea', rx, i_y5, W, H, '#fef3c7', '#b45309')
add_text('ea_t', rx+10, i_y5+6, W-20, H-12, 'Embedding API\nOpenAI / Ark / Local', 10, '#b45309','center','middle','ea')

add_arrow('aI6', *bot(n_ea), rx+W/2, i_y5+H+GAP, '#dc2626')

# Upsert
i_y6 = i_y5+H+GAP
n_up = add_rect('up', rx, i_y6, W, H, '#3b82f6', '#1e3a5f')
add_text('up_t', rx+10, i_y6+6, W-20, H-12, 'Upsert VectorStore\nRedis / Milvus / Qdrant', 10, '#ffffff','center','middle','up')

add_arrow('aI7', *bot(n_up), rx+W/2, i_y6+H+GAP, '#dc2626')

# Graph?
i_y7 = i_y6+H+GAP
nd7 = add_diamond('d7', rx+W/2-DW/2, i_y7, DW, DH, '#ddd6fe', '#6d28d9')
add_text('d7_t', rx+W/2-25, i_y7+DH/2-8, 50, 16, 'Graph?', 10, '#6d28d9','center','middle','d7')

# Yes -> side
add_arrow_path('aI8y', [(rx+W/2+DW/2, i_y7+DH/2), (rx+W+40, i_y7+DH/2)], '#6d28d9')
add_text('aI8yl', rx+W/2+DW/2+5, i_y7+DH/2-18, 25, 12, 'Yes', 9, '#6d28d9')

n_ge = add_rect('ge', rx+W+40, i_y7+DH/2-18, 140, 36, '#ddd6fe', '#6d28d9', 1)
add_text('ge_t', rx+W+48, i_y7+DH/2-12, 124, 24, 'Graph Extract\nEntities \u2192 Neo4j', 8, '#6d28d9','center','middle','ge')

add_arrow('aI8n', rx+W/2, i_y7+DH, rx+W/2, i_y7+DH+GAP, '#dc2626')

# Index complete
i_y8 = i_y7+DH+GAP
n_ic = add_rect('ic', rx, i_y8, W, H, '#a7f3d0', '#047857')
add_text('ic_t', rx+10, i_y8+6, W-20, H-12, 'Index Complete\nReturn fileID', 11, '#047857','center','middle','ic')

# ==============================================================
# GRAPH SEARCH BRANCH (gx=1180)
# ==============================================================
add_text('fl_g', gx, dy_mid-36, W, 16, '\u2462 \u56fe\u68c0\u7d22 (Graph)', 11, '#6d28d9')

g_y0 = dy_mid + 30
# Arrow from diamond bottom -> down a bit -> right -> down to first graph box
diamond_bot_x = cx
diamond_bot_y = y + DH  # bottom of diamond
route_y = diamond_bot_y + 30  # horizontal routing line below diamond
add_arrow_path('aG0', [(diamond_bot_x, diamond_bot_y), (diamond_bot_x, route_y), (gx+W/2, route_y), (gx+W/2, g_y0)], '#6d28d9')
add_text('aG0l', (cx+gx+W/2)/2-40, route_y-18, 100, 14, 'graph_search', 10, '#6d28d9')

n_gs1 = add_rect('gs1', gx, g_y0, W, H, '#ddd6fe', '#6d28d9')
add_text('gs1_t', gx+10, g_y0+6, W-20, H-12, 'Graph Query\nNeo4j Cypher', 11, '#6d28d9','center','middle','gs1')

add_arrow('aG1', *bot(n_gs1), gx+W/2, g_y0+H+GAP, '#6d28d9')

g_y1 = g_y0+H+GAP
n_gs2 = add_rect('gs2', gx, g_y1, W, H, '#ddd6fe', '#6d28d9')
add_text('gs2_t', gx+10, g_y1+6, W-20, H-12, 'Entity + Relations\nSubgraph Walk', 11, '#6d28d9','center','middle','gs2')

add_arrow('aG2', *bot(n_gs2), gx+W/2, g_y1+H+GAP, '#6d28d9')

g_y2 = g_y1+H+GAP
n_gs3 = add_rect('gs3', gx, g_y2, W, H, '#a7f3d0', '#047857')
add_text('gs3_t', gx+10, g_y2+6, W-20, H-12, 'Return Graph\nResults', 11, '#047857','center','middle','gs3')

# ==============================================================
# RESPONSE + END
# ==============================================================
resp_y = max(s_y9+H, i_y8+H, g_y2+H) + 80

# All three branches merge to response
add_arrow_path('aS_back', [bot(n_rr), (lx+W/2, resp_y-20), (cx, resp_y-20), (cx, resp_y)], '#047857')
add_arrow_path('aI_back', [bot(n_ic), (rx+W/2, resp_y-20), (cx+10, resp_y-20), (cx+10, resp_y)], '#047857')
add_arrow_path('aG_back', [bot(n_gs3), (gx+W/2, resp_y-40), (cx+20, resp_y-40), (cx+20, resp_y)], '#047857')

n_resp = add_rect('resp', cx-100, resp_y, 200, H, '#3b82f6', '#1e3a5f', 3)
add_text('resp_t', cx-90, resp_y+6, 180, H-12, 'MCP Response\nJSON-RPC \u2192 Client', 12, '#ffffff','center','middle','resp')

add_arrow('aEnd', *bot(n_resp), cx, resp_y+H+GAP, '#1e3a5f')

n_end = add_ellipse('end', cx-55, resp_y+H+GAP, 110, 40, '#fed7aa', '#c2410c')
add_text('end_t', cx-40, resp_y+H+GAP+8, 80, 24, 'Client', 13, '#c2410c','center','middle','end')

# ==============================================================
# LEGEND
# ==============================================================
leg_y = g_y2 + H + 40
add_rect('leg', gx, leg_y, 200, 130, '#f8fafc', '#94a3b8', 1)
add_text('leg_t', gx+10, leg_y+5, 180, 16, '\u56fe\u4f8b', 12, '#374151','left')
for i,(c,lb) in enumerate([
    ('#3b82f6','\u6838\u5fc3\u670d\u52a1 / \u5b58\u50a8'),('#ddd6fe','Embedding / Rerank'),
    ('#fef3c7','\u53ef\u9009\u589e\u5f3a / API'),('#a7f3d0','\u8fd4\u56de\u7ed3\u679c / \u7f13\u5b58'),
]):
    add_rect(f'lg{i}', gx+10, leg_y+26+i*24, 20, 14, c, '#374151', 1)
    add_text(f'lg{i}t', gx+36, leg_y+26+i*24, 155, 14, lb, 10, '#374151','left')
add_diamond('lgd', gx+8, leg_y+26+4*24, 24, 16, '#fef3c7', '#b45309')
add_text('lgdt', gx+36, leg_y+26+4*24, 155, 14, '\u51b3\u7b56\u5206\u652f', 10, '#374151','left')

# ==============================================================
doc = {'type':'excalidraw','version':2,'source':'https://excalidraw.com',
    'elements':elements,
    'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}

with open('rag-architecture-detail.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc, f, ensure_ascii=False, indent=2)

print(f'OK! {len(elements)} elements written')
