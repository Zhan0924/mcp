# -*- coding: utf-8 -*-
"""Generate detailed Retriever Search Pipeline flowchart."""
import json

elements = []
seed = 500000
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

def add_arrow_path(id, pts, color, sw=2, style='solid'):
    x0,y0 = pts[0]
    rel = [[px-x0, py-y0] for px,py in pts]
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

# Colors
C_BLUE = '#3b82f6'; S_BLUE = '#1e3a5f'
C_GREEN = '#a7f3d0'; S_GREEN = '#047857'
C_YELLOW = '#fef3c7'; S_YELLOW = '#b45309'
C_PURPLE = '#ddd6fe'; S_PURPLE = '#6d28d9'
C_RED = '#fee2e2'; S_RED = '#dc2626'
C_GRAY = '#f1f5f9'; S_GRAY = '#64748b'

# Layout
GAP = 55
W = 240; H = 56
DW = 180; DH = 80
NW = 280  # note width
cx = 400  # main column center
nx = cx + W/2 + 40  # note x (right of main)

# ── TITLE ──
add_text('title', 80, 15, 640, 28, 'RAG Retriever — 检索管线详细流程', 22, '#1e40af')
add_text('sub', 80, 48, 640, 15, 'Retrieve() → QueryValidation → HyDE → MultiQuery → Embed → VectorSearch → Rerank → Compress → Return', 9, S_GRAY)

y = 80

# ── 1. START ──
n0 = add_ellipse('start', cx-60, y, 120, 40, '#fed7aa', '#c2410c')
add_text('start_t', cx-50, y+8, 100, 24, 'Retrieve()', 13, '#c2410c', 'center','middle','start')
add_text('start_f', cx-60, y+44, 120, 12, 'retriever.go', 9, S_GRAY)

add_arrow('a01', *bot(n0), cx, y+40+GAP, S_GREEN)
y += 40+GAP

# ── 2. Query Validation ──
n1 = add_rect('qv', cx-W/2, y, W, H, C_GREEN, S_GREEN)
add_text('qv_t', cx-W/2+5, y+4, W-10, H-8, 'Query Validation\nisValidQuery() + isKnownProbeQuery()', 10, S_GREEN,'center','middle','qv')
add_text('qv_f', cx-W/2, y+H+2, W, 12, 'retriever.go:isValidQuery()', 9, S_GRAY, 'left')
# note
add_rect('qv_n', nx, y-6, NW, 68, C_GRAY, S_GRAY, 1)
add_text('qv_nt', nx+8, y, NW-16, 56, '说明: 拦截无效查询\n• 空白/纯标点 → 返回空\n• "Unknown task" 等探测 → Debug日志\n• 有效字符<2 → 拒绝\n避免浪费 LLM/Embedding API 调用', 9, '#374151','left','top','qv_n')

add_arrow('a02', *bot(n1), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 3. Timeout Budget Check ──
n2 = add_rect('tb', cx-W/2, y, W, H, C_YELLOW, S_YELLOW)
add_text('tb_t', cx-W/2+5, y+4, W-10, H-8, 'Timeout Budget Check\nremainingTime() > 15s?', 10, S_YELLOW,'center','middle','tb')
add_rect('tb_n', nx, y-6, NW, 52, C_GRAY, S_GRAY, 1)
add_text('tb_nt', nx+8, y, NW-16, 40, '说明: 超时预算管理\n各阶段前检查ctx.Deadline剩余\n不足时跳过LLM密集阶段(HyDE/\nMultiQuery), 保证基础检索可用', 9, '#374151','left','top','tb_n')

add_arrow('a03', *bot(n2), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 4. MultiQuery Decision ──
d1 = add_diamond('d_mq', cx-DW/2, y, DW, DH, C_YELLOW, S_YELLOW)
add_text('d_mq_t', cx-36, y+DH/2-8, 72, 16, 'MultiQuery\nEnabled?', 9, S_YELLOW,'center','middle','d_mq')

# Yes path (left)
mq_x = 40
mq_y = y + DH/2
add_arrow_path('a_mq_y', [lft(d1), (mq_x+W/2, mq_y), (mq_x+W/2, mq_y+30)], S_PURPLE)
add_text('a_mq_yl', mq_x+W/2+8, mq_y-16, 25, 12, 'Yes', 9, S_PURPLE)

n_mq = add_rect('mq', mq_x, mq_y+30, W, H, C_PURPLE, S_PURPLE)
add_text('mq_t', mq_x+5, mq_y+34, W-10, H-8, 'MultiQuery: 生成查询变体\nGenerateQueryVariants() → LLM', 10, S_PURPLE,'center','middle','mq')
add_text('mq_f', mq_x, mq_y+30+H+2, W, 12, 'retriever_multiquery.go', 9, S_GRAY, 'left')

n_mq2 = add_rect('mq2', mq_x, mq_y+30+H+GAP, W, H, C_PURPLE, S_PURPLE)
add_text('mq2_t', mq_x+5, mq_y+34+H+GAP, W-10, H-8, 'MultiQueryRetrieve()\n并发检索 + RRF 融合去重', 10, S_PURPLE,'center','middle','mq2')
add_arrow('a_mq1', *bot(n_mq), *top(n_mq2), S_PURPLE)

add_rect('mq_n', mq_x+W+20, mq_y+30, NW, 80, C_GRAY, S_GRAY, 1)
add_text('mq_nt', mq_x+W+28, mq_y+36, NW-16, 68, '说明: 多查询检索 (RAG增强)\n• LLM生成3-5个查询变体\n• 每个变体独立调 Retrieve()\n• inMultiQuery=true 防递归\n• RRF(k=60) 融合多路结果\n• 超时<15s 自动跳过', 9, '#374151','left','top','mq_n')

# No path (down)
add_arrow('a_mq_n', cx, y+DH, cx, y+DH+GAP, S_GREEN)
add_text('a_mq_nl', cx+8, y+DH+6, 20, 12, 'No', 9, S_RED)
y += DH+GAP

# ── 5. HyDE Decision ──
d2 = add_diamond('d_hyde', cx-DW/2, y, DW, DH, C_YELLOW, S_YELLOW)
add_text('d_hyde_t', cx-30, y+DH/2-8, 60, 16, 'HyDE\nEnabled?', 9, S_YELLOW,'center','middle','d_hyde')

# Yes -> right side box
hyde_x = nx
add_arrow_path('a_hy_y', [rgt(d2), (hyde_x+10, y+DH/2), (hyde_x+10, y+DH/2+30)], S_YELLOW)
add_text('a_hy_yl', hyde_x-20, y+DH/2-16, 25, 12, 'Yes', 9, S_YELLOW)

n_hyde = add_rect('hyde', hyde_x-50, y+DH/2+30, W, H, C_YELLOW, S_YELLOW)
add_text('hyde_t', hyde_x-45, y+DH/2+34, W-10, H-8, 'HyDE Transform\nLLM生成假设性文档片段', 10, S_YELLOW,'center','middle','hyde')
add_text('hyde_f', hyde_x-50, y+DH/2+30+H+2, W, 12, 'retriever_hyde.go:Transform()', 9, S_GRAY, 'left')

add_rect('hyde_n', hyde_x+W-30, y+DH/2+20, NW, 68, C_GRAY, S_GRAY, 1)
add_text('hyde_nt', hyde_x+W-22, y+DH/2+26, NW-16, 56, '说明: HyDE(假设文档嵌入)\n• 原始查询 → LLM → 假设性答案\n• 用假设答案做Embedding(非原始Q)\n• 向量空间中更接近真实文档\n• 超时<10s 自动跳过\n• 失败降级用原始查询', 9, '#374151','left','top','hyde_n')

# No -> down
add_arrow('a_hy_n', cx, y+DH, cx, y+DH+GAP, S_GREEN)
add_text('a_hy_nl', cx+8, y+DH+6, 20, 12, 'No', 9, S_RED)
y += DH+GAP

# ── 6. Embed Query ──
n3 = add_rect('eq', cx-W/2, y, W, H, C_PURPLE, S_PURPLE)
add_text('eq_t', cx-W/2+5, y+4, W-10, H-8, 'embedTexts(query)\n三级降级: Cache → Manager → Direct', 10, S_PURPLE,'center','middle','eq')
add_text('eq_f', cx-W/2, y+H+2, W, 12, 'retriever.go:embedTexts()', 9, S_GRAY, 'left')

add_rect('eq_n', nx, y-6, NW, 68, C_GRAY, S_GRAY, 1)
add_text('eq_nt', nx+8, y, NW-16, 56, '说明: 三级降级Embedding\n① GlobalCache → CachedEmbedStrings\n② GlobalManager → 多Provider故障转移\n③ Direct Embedder → 无故障转移兜底\n维度由首次API响应动态推断', 9, '#374151','left','top','eq_n')

add_arrow('a04', *bot(n3), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 7. Build Filter ──
n4 = add_rect('bf', cx-W/2, y, W, H, C_BLUE, S_BLUE)
add_text('bf_t', cx-W/2+5, y+4, W-10, H-8, 'Build RediSearch Filter\n@file_id:{id1|id2} or *', 10, '#ffffff','center','middle','bf')
add_text('bf_f', cx-W/2, y+H+2, W, 12, 'retriever.go:escapeTagValue()', 9, S_GRAY, 'left')

add_rect('bf_n', nx, y-6, NW, 52, C_GRAY, S_GRAY, 1)
add_text('bf_nt', nx+8, y, NW-16, 40, '说明: 多租户+文件过滤\n• userID → 独立索引名(隔离)\n• fileIDs → TAG过滤表达式\n• 特殊字符转义防注入', 9, '#374151','left','top','bf_n')

add_arrow('a05', *bot(n4), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 8. Hybrid Decision ──
d3 = add_diamond('d_hyb', cx-DW/2, y, DW, DH, C_BLUE, S_BLUE)
add_text('d_hyb_t', cx-30, y+DH/2-8, 60, 16, 'Hybrid\nSearch?', 9, S_BLUE,'center','middle','d_hyb')

# Yes -> left: hybrid
hx = 30
add_arrow_path('a_hyb_y', [lft(d3), (hx+100, y+DH/2), (hx+100, y+DH/2+30)], S_BLUE)
add_text('a_hyb_yl', hx+108, y+DH/2-16, 25, 12, 'Yes', 9, S_BLUE)

n_hyb = add_rect('hyb', hx, y+DH/2+30, 200, H, C_BLUE, S_BLUE)
add_text('hyb_t', hx+5, y+DH/2+34, 190, H-8, 'Vector KNN + BM25\nhybridRetrieve()', 10, '#ffffff','center','middle','hyb')

n_hyb2 = add_rect('hyb2', hx, y+DH/2+30+H+GAP, 200, H, C_BLUE, S_BLUE)
add_text('hyb2_t', hx+5, y+DH/2+34+H+GAP, 190, H-8, 'RRF Merge\nTopK×3 过采样→截断', 10, '#ffffff','center','middle','hyb2')
add_arrow('a_hyb1', *bot(n_hyb), *top(n_hyb2), S_BLUE)

add_rect('hyb_n', hx-10, y+DH/2+30+2*(H+GAP)-10, 220, 56, C_GRAY, S_GRAY, 1)
add_text('hyb_nt', hx-2, y+DH/2+30+2*(H+GAP)-4, 204, 44, '说明: 混合检索\n• Vector权重+Keyword权重加权\n• 过采样3x保证融合质量\n• 失败降级为纯向量检索', 9, '#374151','left','top')

# No -> right: pure vector
add_arrow_path('a_hyb_n', [rgt(d3), (nx+20, y+DH/2), (nx+20, y+DH/2+30)], S_BLUE)
add_text('a_hyb_nl', nx-20, y+DH/2-16, 20, 12, 'No', 9, S_RED)

n_vec = add_rect('vec', nx-50, y+DH/2+30, 200, H, C_BLUE, S_BLUE)
add_text('vec_t', nx-45, y+DH/2+34, 190, H-8, 'Pure Vector Search\nFT.SEARCH ... KNN TopK', 10, '#ffffff','center','middle','vec')

# merge both to dedup
merge_y = y + DH/2 + 30 + 2*(H+GAP) + 50
add_arrow_path('a_merge1', [bot(n_hyb2), (hx+100, merge_y-10), (cx, merge_y-10), (cx, merge_y)], S_GREEN)
add_arrow_path('a_merge2', [bot(n_vec), (nx+50, merge_y-10), (cx+5, merge_y-10), (cx+5, merge_y)], S_GREEN)

y = merge_y

# ── 9. Dedup ──
n5 = add_rect('dedup', cx-W/2, y, W, H, C_GREEN, S_GREEN)
add_text('dedup_t', cx-W/2+5, y+4, W-10, H-8, 'deduplicateByParent()\nParent-Child 模式去重', 10, S_GREEN,'center','middle','dedup')

add_rect('dedup_n', nx, y-6, NW, 52, C_GRAY, S_GRAY, 1)
add_text('dedup_nt', nx+8, y, NW-16, 40, '说明: 父子块去重\n• 多个子块命中同一父块时\n  只保留相关性最高的一个\n• 非Parent-Child结果直接保留', 9, '#374151','left','top','dedup_n')

add_arrow('a06', *bot(n5), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 10. MinScore Filter ──
n6 = add_rect('msf', cx-W/2, y, W, H, C_GREEN, S_GREEN)
add_text('msf_t', cx-W/2+5, y+4, W-10, H-8, 'filterByMinScore()\nscore = 1 - distance/2', 10, S_GREEN,'center','middle','msf')

add_rect('msf_n', nx, y-6, NW, 56, C_GRAY, S_GRAY, 1)
add_text('msf_nt', nx+8, y, NW-16, 44, '说明: 最低分阈值过滤\n• 余弦距离[0,2]→分数[0,1]\n• 低于MinScore的结果丢弃\n• 减少给LLM的噪声上下文', 9, '#374151','left','top','msf_n')

add_arrow('a07', *bot(n6), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 11. Rerank Decision ──
d4 = add_diamond('d_rr', cx-DW/2, y, DW, DH, C_PURPLE, S_PURPLE)
add_text('d_rr_t', cx-30, y+DH/2-8, 60, 16, 'Rerank\nEnabled?', 9, S_PURPLE,'center','middle','d_rr')

add_arrow('a_rr_y', cx, y+DH, cx, y+DH+GAP, S_PURPLE)
add_text('a_rr_yl', cx+8, y+DH+6, 25, 12, 'Yes', 9, S_PURPLE)

y += DH+GAP
n_rr = add_rect('rr', cx-W/2, y, W, H, C_PURPLE, S_PURPLE)
add_text('rr_t', cx-W/2+5, y+4, W-10, H-8, 'Reranker (Cross-Encoder)\nDashScope / Qwen3 Rerank API', 10, S_PURPLE,'center','middle','rr')
add_text('rr_f', cx-W/2, y+H+2, W, 12, 'retriever_reranker.go', 9, S_GRAY, 'left')

add_rect('rr_n', nx, y-6, NW, 68, C_GRAY, S_GRAY, 1)
add_text('rr_nt', nx+8, y, NW-16, 56, '说明: 交叉编码器重排序\n• 对(query,doc)对做精排\n• Cross-Encoder比Bi-Encoder精度高\n• 但延迟大,只对TopK候选做\n• 支持DashScope/自定义API\n• 失败降级返回原序结果', 9, '#374151','left','top','rr_n')

add_arrow('a08', *bot(n_rr), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 12. Compress ──
n_cc = add_rect('cc', cx-W/2, y, W, H, C_YELLOW, S_YELLOW)
add_text('cc_t', cx-W/2+5, y+4, W-10, H-8, 'Context Compression\napplyContextCompression()', 10, S_YELLOW,'center','middle','cc')
add_text('cc_f', cx-W/2, y+H+2, W, 12, 'retriever_compressor.go:CompressResults()', 9, S_GRAY, 'left')

add_rect('cc_n', nx, y-6, NW, 68, C_GRAY, S_GRAY, 1)
add_text('cc_nt', nx+8, y, NW-16, 56, '说明: LLM上下文压缩\n• 提取与query最相关的段落\n• 去除冗余/不相关内容\n• 减少Token消耗提高回答质量\n• 超时<5s 自动跳过\n• 失败降级返回原始结果', 9, '#374151','left','top','cc_n')

add_arrow('a09', *bot(n_cc), cx, y+H+GAP, S_GREEN)
y += H+GAP

# ── 13. Return ──
n_ret = add_rect('ret', cx-W/2, y, W, H, C_GREEN, S_GREEN, 3)
add_text('ret_t', cx-W/2+5, y+4, W-10, H-8, 'Return []RetrievalResult\n{Content, FileID, Score, ChunkID...}', 10, S_GREEN,'center','middle','ret')

add_arrow('a10', *bot(n_ret), cx, y+H+30, '#c2410c')
n_end = add_ellipse('end', cx-50, y+H+30, 100, 36, '#fed7aa', '#c2410c')
add_text('end_t', cx-38, y+H+36, 76, 24, 'Caller', 12, '#c2410c','center','middle','end')

# ── Save ──
doc = {'type':'excalidraw','version':2,'source':'https://excalidraw.com',
    'elements':elements,
    'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}

with open('module-retriever.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc, f, ensure_ascii=False, indent=2)
print(f'OK! {len(elements)} elements → module-retriever.excalidraw')
