# -*- coding: utf-8 -*-
"""Generate a proper flowchart for MCP RAG Server with precise arrow coordinates."""
import json

elements = []
seed = 300000
def ns():
    global seed; seed += 1; return seed

# ── element factories ─────────────────────────────────────────
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
    return (x, y, w, h)

def add_diamond(id,x,y,w,h,fill,stroke):
    elements.append({'type':'diamond','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid',
        'strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x, y, w, h)

def add_ellipse(id,x,y,w,h,fill,stroke):
    elements.append({'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid',
        'strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':[],'link':None,'locked':False})
    return (x, y, w, h)

def add_arrow(id, x1, y1, x2, y2, color, sw=2, style='solid'):
    """Arrow from absolute point (x1,y1) to absolute point (x2,y2)."""
    dx = x2 - x1; dy = y2 - y1
    elements.append({'type':'arrow','id':id,'x':x1,'y':y1,
        'width':abs(dx),'height':abs(dy),
        'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'points':[[0,0],[dx,dy]],
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})

def add_arrow_path(id, pts_abs, color, sw=2, style='solid'):
    """Arrow through multiple absolute points."""
    x0, y0 = pts_abs[0]
    rel = [[px - x0, py - y0] for px, py in pts_abs]
    w = max(abs(p[0]) for p in rel) if rel else 0
    h = max(abs(p[1]) for p in rel) if rel else 0
    elements.append({'type':'arrow','id':id,'x':x0,'y':y0,'width':w,'height':h,
        'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,
        'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,
        'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'points':rel,
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})

# ── helpers for box edge midpoints ────────────────────────────
def mid_top(b):    return (b[0]+b[2]/2, b[1])
def mid_bot(b):    return (b[0]+b[2]/2, b[1]+b[3])
def mid_left(b):   return (b[0],        b[1]+b[3]/2)
def mid_right(b):  return (b[0]+b[2],   b[1]+b[3]/2)

GAP = 28  # vertical gap between steps
W = 180; H = 48; DW = 170; DH = 80

# ==============================================================
# TITLE
# ==============================================================
add_text('title', 120, 10, 700, 32, 'MCP RAG Server \u2014 \u6838\u5fc3\u6d41\u7a0b\u56fe', 24, '#1e40af')
add_text('sub', 120, 42, 700, 16, '\u4ece MCP Client \u8bf7\u6c42 \u2192 Tool \u5206\u53d1 \u2192 Search / Index / Graph \u2192 \u54cd\u5e94\u8fd4\u56de', 11, '#64748b')

# ==============================================================
# MAIN TRUNK (center column cx=400)
# ==============================================================
cx = 400

# Start
n_start = add_ellipse('start', cx-65, 70, 130, 45, '#fed7aa', '#c2410c')
add_text('start_t', cx-55, 80, 110, 25, 'MCP Client', 13, '#c2410c', 'center', 'middle', 'start')

add_arrow('a01', *mid_bot(n_start), cx, 70+45+GAP, '#c2410c')
add_text('a01l', cx+8, 120, 100, 14, 'HTTP POST /mcp', 9, '#64748b', 'left')

# HTTP Handler
y1 = 70+45+GAP
n_http = add_rect('http', cx-W/2, y1, W, H, '#3b82f6', '#1e3a5f')
add_text('http_t', cx-W/2+10, y1+6, W-20, H-12, 'Streamable HTTP\nserver.go', 11, '#ffffff', 'center', 'middle', 'http')

add_arrow('a02', *mid_bot(n_http), *mid_top((cx-W/2, y1+H+GAP, W, H)), '#1e3a5f')

# MCP Server
y2 = y1+H+GAP
n_srv = add_rect('srv', cx-W/2, y2, W, H, '#3b82f6', '#1e3a5f', 3)
add_text('srv_t', cx-W/2+10, y2+6, W-20, H-12, 'MCP Server\nJSON-RPC dispatch', 11, '#ffffff', 'center', 'middle', 'srv')

add_arrow('a03', *mid_bot(n_srv), *mid_top((cx-W/2, y2+H+GAP, W, H)), '#1e3a5f')

# Registry
y3 = y2+H+GAP
n_reg = add_rect('reg', cx-W/2, y3, W, H, '#60a5fa', '#1e3a5f')
add_text('reg_t', cx-W/2+10, y3+6, W-20, H-12, 'Registry\nApplyToServer()', 11, '#ffffff', 'center', 'middle', 'reg')

add_arrow('a04', *mid_bot(n_reg), cx, y3+H+GAP, '#1e3a5f')

# Decision: Tool Type
y4 = y3+H+GAP
n_d1 = add_diamond('d1', cx-DW/2, y4, DW, DH, '#fef3c7', '#b45309')
add_text('d1_t', cx-40, y4+DH/2-8, 80, 16, 'Tool Type?', 11, '#b45309', 'center', 'middle', 'd1')

# ==============================================================
# LEFT: Search Flow (lx=100)
# ==============================================================
lx = 100
sy = y4 + DH/2  # diamond left edge mid-y

# Arrow from diamond left to first search box
search_y0 = y4 + 10
add_arrow_path('aL0', [(cx-DW/2, sy), (lx+W, sy)], '#047857')
add_text('aL0l', lx+W+5, sy-18, 80, 14, 'search', 10, '#047857')

# Search label
add_text('fl_s', lx-10, sy-20, 200, 14, '\u2460 \u68c0\u7d22\u6d41\u7a0b', 11, '#047857')

# Query Validation
sy1 = sy + 20
n_qv = add_rect('qv', lx, sy1, W, H, '#a7f3d0', '#047857')
add_text('qv_t', lx+10, sy1+6, W-20, H-12, 'Query Validation\nisValidQuery()', 11, '#047857', 'center', 'middle', 'qv')

add_arrow('aS1', *mid_bot(n_qv), lx+W/2, sy1+H+GAP, '#047857')

# Diamond: HyDE?
sy2 = sy1+H+GAP
n_d2 = add_diamond('d2', lx+W/2-DW/2, sy2, DW, DH, '#fef3c7', '#b45309')
add_text('d2_t', lx+W/2-30, sy2+DH/2-8, 60, 16, 'HyDE?', 10, '#b45309', 'center', 'middle', 'd2')

# Yes -> HyDE
add_arrow('aS2y', lx+W/2, sy2+DH, lx+W/2, sy2+DH+GAP, '#047857')
add_text('aS2yl', lx+W/2+6, sy2+DH+4, 25, 12, 'Yes', 9, '#047857', 'left')

sy3 = sy2+DH+GAP
n_hyde = add_rect('hyde', lx, sy3, W, H, '#fef3c7', '#b45309')
add_text('hyde_t', lx+10, sy3+6, W-20, H-12, 'HyDE / MultiQuery\nTransform()', 11, '#b45309', 'center', 'middle', 'hyde')

# No -> bypass to Embed
add_arrow_path('aS2n', [(lx+W/2-DW/2, sy2+DH/2), (lx-30, sy2+DH/2), (lx-30, sy3+H+GAP+H/2), (lx, sy3+H+GAP+H/2)], '#64748b', 1, 'dashed')
add_text('aS2nl', lx-50, sy2+DH/2-14, 20, 12, 'No', 9, '#dc2626')

add_arrow('aS3', *mid_bot(n_hyde), lx+W/2, sy3+H+GAP, '#047857')

# Embed Query
sy4 = sy3+H+GAP
n_eq = add_rect('eq', lx, sy4, W, H, '#ddd6fe', '#6d28d9')
add_text('eq_t', lx+10, sy4+6, W-20, H-12, 'Embed Query\nEmbedStrings()', 11, '#6d28d9', 'center', 'middle', 'eq')

add_arrow('aS4', *mid_bot(n_eq), lx+W/2, sy4+H+GAP, '#047857')

# Diamond: Hybrid?
sy5 = sy4+H+GAP
n_d3 = add_diamond('d3', lx+W/2-DW/2, sy5, DW, DH, '#93c5fd', '#1e3a5f')
add_text('d3_t', lx+W/2-28, sy5+DH/2-8, 56, 16, 'Hybrid?', 10, '#1e3a5f', 'center', 'middle', 'd3')

# Two branches: Vector (left-down) and Keyword (right-down)
vx = lx - 80; kx = lx + W + 30
sby = sy5 + DH + 20

add_arrow_path('aH1', [(lx+W/2-DW/2, sy5+DH/2), (vx+80, sy5+DH/2), (vx+80, sby)], '#1e3a5f')
add_text('aH1l', vx+40, sy5+DH/2-16, 30, 12, 'No', 9, '#dc2626')

n_vs = add_rect('vs', vx, sby, 160, H, '#3b82f6', '#1e3a5f')
add_text('vs_t', vx+10, sby+6, 140, H-12, 'Vector Search\nFT.SEARCH KNN', 11, '#ffffff', 'center', 'middle', 'vs')

add_arrow_path('aH2', [(lx+W/2+DW/2, sy5+DH/2), (kx, sy5+DH/2), (kx, sby)], '#1e3a5f')
add_text('aH2l', kx+5, sy5+DH/2-16, 30, 12, 'Yes', 9, '#047857')

n_ks = add_rect('ks', kx-80, sby, 160, H, '#93c5fd', '#1e3a5f')
add_text('ks_t', kx-70, sby+6, 140, H-12, 'Keyword Search\nBM25 text', 11, '#1e3a5f', 'center', 'middle', 'ks')

# Merge to RRF
rrf_y = sby + H + GAP
add_arrow_path('aM1', [mid_bot(n_vs), (vx+80, rrf_y-8), (lx+W/2, rrf_y-8), (lx+W/2, rrf_y)], '#1e3a5f')
add_arrow_path('aM2', [mid_bot(n_ks), (kx, rrf_y-8), (lx+W/2, rrf_y-8), (lx+W/2, rrf_y)], '#1e3a5f')

n_rrf = add_rect('rrf', lx, rrf_y, W, H, '#93c5fd', '#1e3a5f')
add_text('rrf_t', lx+10, rrf_y+6, W-20, H-12, 'RRF Merge\nmergeByRRF()', 11, '#1e3a5f', 'center', 'middle', 'rrf')

add_arrow('aS5', *mid_bot(n_rrf), lx+W/2, rrf_y+H+GAP, '#047857')

# Diamond: Rerank?
sy6 = rrf_y+H+GAP
n_d4 = add_diamond('d4', lx+W/2-DW/2, sy6, DW, DH, '#ddd6fe', '#6d28d9')
add_text('d4_t', lx+W/2-28, sy6+DH/2-8, 56, 16, 'Rerank?', 10, '#6d28d9', 'center', 'middle', 'd4')

# Yes
add_arrow('aS6y', lx+W/2, sy6+DH, lx+W/2, sy6+DH+GAP, '#047857')
add_text('aS6yl', lx+W/2+6, sy6+DH+4, 25, 12, 'Yes', 9, '#047857', 'left')

sy7 = sy6+DH+GAP
n_rrk = add_rect('rrk', lx, sy7, W, H, '#ddd6fe', '#6d28d9')
add_text('rrk_t', lx+10, sy7+6, W-20, H-12, 'Reranker\nDashScope / Qwen3', 11, '#6d28d9', 'center', 'middle', 'rrk')

# No -> bypass
add_arrow_path('aS6n', [(lx+W/2-DW/2, sy6+DH/2), (lx-30, sy6+DH/2), (lx-30, sy7+H+GAP+H/2), (lx, sy7+H+GAP+H/2)], '#64748b', 1, 'dashed')
add_text('aS6nl', lx-50, sy6+DH/2-14, 20, 12, 'No', 9, '#dc2626')

add_arrow('aS7', *mid_bot(n_rrk), lx+W/2, sy7+H+GAP, '#047857')

# Context Compress
sy8 = sy7+H+GAP
n_cc = add_rect('cc', lx, sy8, W, H, '#fef3c7', '#b45309')
add_text('cc_t', lx+10, sy8+6, W-20, H-12, 'Context Compress\nCompress()', 11, '#b45309', 'center', 'middle', 'cc')

add_arrow('aS8', *mid_bot(n_cc), lx+W/2, sy8+H+GAP, '#047857')

# Return results
sy9 = sy8+H+GAP
n_rr = add_rect('rr', lx, sy9, W, H, '#a7f3d0', '#047857')
add_text('rr_t', lx+10, sy9+6, W-20, H-12, 'Return Results\n[]RetrievalResult', 11, '#047857', 'center', 'middle', 'rr')

# Arrow to response (bottom)
resp_y = max(sy9 + H + 80, 1250)
add_arrow_path('aS_back', [mid_bot(n_rr), (lx+W/2, resp_y-30), (cx, resp_y-30), (cx, resp_y)], '#047857')

# ==============================================================
# RIGHT: Index Flow (rx=700)
# ==============================================================
rx = 700
iy = y4 + DH/2

add_arrow_path('aR0', [(cx+DW/2, iy), (rx, iy)], '#dc2626')
add_text('aR0l', cx+DW/2+10, iy-18, 90, 14, 'index / upload', 10, '#dc2626')
add_text('fl_i', rx-10, iy-20, 200, 14, '\u2461 \u7d22\u5f15\u6d41\u7a0b', 11, '#dc2626')

# Parse
iy1 = iy + 20
n_pd = add_rect('pd', rx, iy1, W, H, '#93c5fd', '#1e3a5f')
add_text('pd_t', rx+10, iy1+6, W-20, H-12, 'Parse Document\nMD / HTML / PDF / DOCX', 10, '#1e3a5f', 'center', 'middle', 'pd')

add_arrow('aI1', *mid_bot(n_pd), rx+W/2, iy1+H+GAP, '#dc2626')

# Chunk
iy2 = iy1+H+GAP
n_ct = add_rect('ct', rx, iy2, W, H, '#93c5fd', '#1e3a5f')
add_text('ct_t', rx+10, iy2+6, W-20, H-12, 'Chunk Text\nStructure / Semantic / Code', 10, '#1e3a5f', 'center', 'middle', 'ct')

add_arrow('aI2', *mid_bot(n_ct), rx+W/2, iy2+H+GAP, '#dc2626')

# Diamond: Async?
iy3 = iy2+H+GAP
n_d5 = add_diamond('d5', rx+W/2-DW/2, iy3, DW, DH, '#fee2e2', '#dc2626')
add_text('d5_t', rx+W/2-25, iy3+DH/2-8, 50, 16, 'Async?', 10, '#dc2626', 'center', 'middle', 'd5')

# Yes -> queue (right)
add_arrow_path('aI3y', [(rx+W/2+DW/2, iy3+DH/2), (rx+W+60, iy3+DH/2)], '#dc2626')
add_text('aI3yl', rx+W/2+DW/2+5, iy3+DH/2-16, 25, 12, 'Yes', 9, '#dc2626')

n_wq = add_rect('wq', rx+W+60, iy3+DH/2-25, 150, 50, '#fee2e2', '#dc2626', 1)
add_text('wq_t', rx+W+70, iy3+DH/2-19, 130, 38, 'Redis Queue\nWorker Pool (\u5f02\u6b65)', 9, '#dc2626', 'center', 'middle', 'wq')

# No -> continue down
add_arrow('aI3n', rx+W/2, iy3+DH, rx+W/2, iy3+DH+GAP, '#dc2626')
add_text('aI3nl', rx+W/2+6, iy3+DH+4, 20, 12, 'No', 9, '#64748b', 'left')

# Embed Chunks
iy4 = iy3+DH+GAP
n_ec = add_rect('ec', rx, iy4, W, H, '#ddd6fe', '#6d28d9')
add_text('ec_t', rx+10, iy4+6, W-20, H-12, 'Embed Chunks\nEmbedStrings()', 11, '#6d28d9', 'center', 'middle', 'ec')

add_arrow('aI4', *mid_bot(n_ec), rx+W/2, iy4+H+GAP, '#dc2626')

# Diamond: Cache?
iy5 = iy4+H+GAP
n_d6 = add_diamond('d6', rx+W/2-DW/2, iy5, DW, DH, '#a7f3d0', '#047857')
add_text('d6_t', rx+W/2-25, iy5+DH/2-8, 50, 16, 'Cache?', 10, '#047857', 'center', 'middle', 'd6')

# Hit -> side
add_arrow_path('aI5h', [(rx+W/2+DW/2, iy5+DH/2), (rx+W+60, iy5+DH/2)], '#047857')
add_text('aI5hl', rx+W/2+DW/2+5, iy5+DH/2-16, 50, 12, 'Hit', 9, '#047857')
n_ch = add_rect('ch', rx+W+60, iy5+DH/2-20, 130, 40, '#a7f3d0', '#047857', 1)
add_text('ch_t', rx+W+70, iy5+DH/2-14, 110, 28, 'Cache Return\nLRU / Redis', 9, '#047857', 'center', 'middle', 'ch')

# Miss -> API
add_arrow('aI5m', rx+W/2, iy5+DH, rx+W/2, iy5+DH+GAP, '#dc2626')
add_text('aI5ml', rx+W/2+6, iy5+DH+4, 30, 12, 'Miss', 9, '#dc2626', 'left')

iy6 = iy5+DH+GAP
n_ea = add_rect('ea', rx, iy6, W, H, '#fef3c7', '#b45309')
add_text('ea_t', rx+10, iy6+6, W-20, H-12, 'Embedding API\nOpenAI / Ark / Local', 10, '#b45309', 'center', 'middle', 'ea')

add_arrow('aI6', *mid_bot(n_ea), rx+W/2, iy6+H+GAP, '#dc2626')

# Upsert VectorStore
iy7 = iy6+H+GAP
n_up = add_rect('up', rx, iy7, W, H, '#3b82f6', '#1e3a5f')
add_text('up_t', rx+10, iy7+6, W-20, H-12, 'Upsert VectorStore\nRedis / Milvus / Qdrant', 10, '#ffffff', 'center', 'middle', 'up')

add_arrow('aI7', *mid_bot(n_up), rx+W/2, iy7+H+GAP, '#dc2626')

# Diamond: Graph?
iy8 = iy7+H+GAP
n_d7 = add_diamond('d7', rx+W/2-DW/2, iy8, DW, DH, '#ddd6fe', '#6d28d9')
add_text('d7_t', rx+W/2-25, iy8+DH/2-8, 50, 16, 'Graph?', 10, '#6d28d9', 'center', 'middle', 'd7')

# Yes -> side
add_arrow_path('aI8y', [(rx+W/2+DW/2, iy8+DH/2), (rx+W+60, iy8+DH/2)], '#6d28d9')
add_text('aI8yl', rx+W/2+DW/2+5, iy8+DH/2-16, 25, 12, 'Yes', 9, '#6d28d9')
n_ge = add_rect('ge', rx+W+60, iy8+DH/2-20, 150, 40, '#ddd6fe', '#6d28d9', 1)
add_text('ge_t', rx+W+70, iy8+DH/2-14, 130, 28, 'Graph Extract\nEntities \u2192 Neo4j', 9, '#6d28d9', 'center', 'middle', 'ge')

# No -> complete
add_arrow('aI8n', rx+W/2, iy8+DH, rx+W/2, iy8+DH+GAP, '#dc2626')

iy9 = iy8+DH+GAP
n_ic = add_rect('ic', rx, iy9, W, H, '#a7f3d0', '#047857')
add_text('ic_t', rx+10, iy9+6, W-20, H-12, 'Index Complete\nReturn fileID', 11, '#047857', 'center', 'middle', 'ic')

# Arrow to response
add_arrow_path('aI_back', [mid_bot(n_ic), (rx+W/2, resp_y-30), (cx, resp_y-30), (cx, resp_y)], '#047857')

# ==============================================================
# GRAPH SEARCH BRANCH (far right gx=1020)
# ==============================================================
gx = 1020
gy = y4 + DH/4  # diamond top-right area

# Arrow from diamond top-right down-right
add_arrow_path('aG0', [(cx+DW/2, sy-DH/4), (gx+W/2, sy-DH/4), (gx+W/2, sy+20)], '#6d28d9')
add_text('aG0l', (cx+DW/2+gx+W/2)/2-40, sy-DH/4-18, 100, 14, 'graph_search', 10, '#6d28d9')
add_text('fl_g', gx, sy, 180, 14, '\u2462 \u56fe\u68c0\u7d22', 11, '#6d28d9')

gy1 = sy + 20 + 4
n_gs1 = add_rect('gs1', gx, gy1, W, H, '#ddd6fe', '#6d28d9')
add_text('gs1_t', gx+10, gy1+6, W-20, H-12, 'Graph Query\nNeo4j Cypher', 11, '#6d28d9', 'center', 'middle', 'gs1')

add_arrow('aG1', *mid_bot(n_gs1), gx+W/2, gy1+H+GAP, '#6d28d9')

gy2 = gy1+H+GAP
n_gs2 = add_rect('gs2', gx, gy2, W, H, '#ddd6fe', '#6d28d9')
add_text('gs2_t', gx+10, gy2+6, W-20, H-12, 'Entity + Relations\nSubgraph Walk', 11, '#6d28d9', 'center', 'middle', 'gs2')

add_arrow('aG2', *mid_bot(n_gs2), gx+W/2, gy2+H+GAP, '#6d28d9')

gy3 = gy2+H+GAP
n_gs3 = add_rect('gs3', gx, gy3, W, H, '#a7f3d0', '#047857')
add_text('gs3_t', gx+10, gy3+6, W-20, H-12, 'Return Graph\nResults', 11, '#047857', 'center', 'middle', 'gs3')

# Arrow back to response
add_arrow_path('aG_back', [mid_bot(n_gs3), (gx+W/2, resp_y-50), (cx+30, resp_y-50), (cx+30, resp_y)], '#047857')

# ==============================================================
# RESPONSE + END
# ==============================================================
n_resp = add_rect('resp', cx-100, resp_y, 200, 50, '#3b82f6', '#1e3a5f', 3)
add_text('resp_t', cx-90, resp_y+6, 180, 38, 'MCP Response\nJSON-RPC \u2192 Client', 12, '#ffffff', 'center', 'middle', 'resp')

add_arrow('aEnd', *mid_bot(n_resp), cx, resp_y+50+GAP, '#1e3a5f')

n_end = add_ellipse('end', cx-55, resp_y+50+GAP, 110, 40, '#fed7aa', '#c2410c')
add_text('end_t', cx-40, resp_y+50+GAP+8, 80, 24, 'Client', 13, '#c2410c', 'center', 'middle', 'end')

# ==============================================================
# LEGEND (far right bottom)
# ==============================================================
ly = 900
add_rect('leg', 1020, ly, 200, 120, '#f8fafc', '#94a3b8', 1)
add_text('leg_t', 1030, ly+5, 180, 16, '\u56fe\u4f8b', 12, '#374151', 'left')
for i, (c, lb) in enumerate([
    ('#3b82f6', '\u6838\u5fc3\u670d\u52a1 / \u5b58\u50a8'),
    ('#ddd6fe', 'Embedding / Rerank'),
    ('#fef3c7', '\u53ef\u9009\u589e\u5f3a / API'),
    ('#a7f3d0', '\u8fd4\u56de\u7ed3\u679c / \u7f13\u5b58'),
]):
    add_rect(f'lg{i}', 1030, ly+25+i*22, 20, 14, c, '#374151', 1)
    add_text(f'lg{i}t', 1055, ly+25+i*22, 160, 14, lb, 10, '#374151', 'left')

add_diamond(f'lgd', 1028, ly+25+4*22, 24, 16, '#fef3c7', '#b45309')
add_text(f'lgdt', 1055, ly+25+4*22, 160, 14, '\u51b3\u7b56\u5206\u652f', 10, '#374151', 'left')

# ==============================================================
# WRITE FILE
# ==============================================================
doc = {
    'type': 'excalidraw', 'version': 2,
    'source': 'https://excalidraw.com',
    'elements': elements,
    'appState': {'viewBackgroundColor': '#ffffff', 'gridSize': 20},
    'files': {}
}

with open('rag-architecture-detail.excalidraw', 'w', encoding='utf-8') as f:
    json.dump(doc, f, ensure_ascii=False, indent=2)

print(f'OK! {len(elements)} elements')
