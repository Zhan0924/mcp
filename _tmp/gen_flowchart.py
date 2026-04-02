# -*- coding: utf-8 -*-
import json

elements = []
seed = 200000

def s():
    global seed; seed += 1; return seed

def txt(id,x,y,w,h,t,sz=14,c='#374151',a='center',va='top',cid=None):
    return {'type':'text','id':id,'x':x,'y':y,'width':w,'height':h,'text':t,'originalText':t,
        'fontSize':sz,'fontFamily':3,'textAlign':a,'verticalAlign':va,'strokeColor':c,
        'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':1,'strokeStyle':'solid',
        'roughness':0,'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),
        'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'containerId':cid,'lineHeight':1.25}

def box(id,x,y,w,h,f,st,sw=2,ss='solid',be=None,r=3):
    return {'type':'rectangle','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':st,
        'backgroundColor':f,'fillStyle':'solid','strokeWidth':sw,'strokeStyle':ss,'roughness':0,
        'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,
        'groupIds':[],'boundElements':be or [],'link':None,'locked':False,'roundness':{'type':r}}

def diamond(id,x,y,w,h,f,st,be=None):
    return {'type':'diamond','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':st,
        'backgroundColor':f,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,
        'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,
        'groupIds':[],'boundElements':be or [],'link':None,'locked':False}

def ell(id,x,y,w,h,f,st,be=None):
    return {'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':st,
        'backgroundColor':f,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,
        'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,
        'groupIds':[],'boundElements':be or [],'link':None,'locked':False}

def arr(id,x,y,dx,dy,st,sw=2,ss='solid',ea='arrow'):
    return {'type':'arrow','id':id,'x':x,'y':y,'width':abs(dx),'height':abs(dy),
        'strokeColor':st,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':ss,'roughness':0,'opacity':100,'angle':0,
        'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,'groupIds':[],
        'boundElements':None,'link':None,'locked':False,
        'points':[[0,0],[dx,dy]],
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':ea}

def arr2(id,x,y,pts,st,sw=2,ss='solid',ea='arrow'):
    w = max(abs(p[0]) for p in pts) if pts else 0
    h = max(abs(p[1]) for p in pts) if pts else 0
    return {'type':'arrow','id':id,'x':x,'y':y,'width':w,'height':h,
        'strokeColor':st,'backgroundColor':'transparent','fillStyle':'solid',
        'strokeWidth':sw,'strokeStyle':ss,'roughness':0,'opacity':100,'angle':0,
        'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,'groupIds':[],
        'boundElements':None,'link':None,'locked':False,
        'points':[[0,0]]+pts,
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':ea}

def ln(id,x,y,pts,st,sw=1,ss='dashed'):
    return {'type':'line','id':id,'x':x,'y':y,'width':0,'height':0,'strokeColor':st,
        'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':ss,
        'roughness':0,'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),
        'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'points':[[0,0]]+pts}

e = elements
W = 180  # standard box width
H = 50   # standard box height

# ============================================================
# TITLE
# ============================================================
e.append(txt('title', 200, 10, 900, 36, 'MCP RAG Server \u2014 \u6838\u5fc3\u6d41\u7a0b\u56fe', 26, '#1e40af'))
e.append(txt('sub', 200, 46, 900, 16, '\u4ece\u5ba2\u6237\u7aef\u8bf7\u6c42\u5230\u5b8c\u6210\u54cd\u5e94\u7684\u5b8c\u6574\u6570\u636e\u6d41\u8f6c', 12, '#64748b'))

# ============================================================
# FLOW 1: MAIN REQUEST PATH (center column, x=350)
# ============================================================
cx = 410  # center x for main flow

# 1. START: MCP Client
e.append(ell('start', cx-70, 80, 140, 50, '#fed7aa', '#c2410c'))
e.append(txt('start_t', cx-55, 93, 110, 24, 'MCP Client', 14, '#c2410c', 'center', 'middle', 'start'))

# Arrow down
e.append(arr('a1', cx, 130, 0, 30, '#c2410c'))
e.append(txt('a1l', cx+5, 133, 120, 14, 'HTTP POST /mcp', 9, '#64748b', 'left'))

# 2. Streamable HTTP Handler
e.append(box('http', cx-90, 162, W, H, '#3b82f6', '#1e3a5f'))
e.append(txt('http_t', cx-80, 170, 160, 34, 'Streamable HTTP\nserver.go', 12, '#ffffff', 'center', 'middle', 'http'))

e.append(arr('a2', cx, 212, 0, 30, '#1e3a5f'))

# 3. MCP Server (mcp-go)
e.append(box('srv', cx-90, 244, W, H, '#3b82f6', '#1e3a5f', 3))
e.append(txt('srv_t', cx-80, 252, 160, 34, 'MCP Server\nJSON-RPC dispatch', 12, '#ffffff', 'center', 'middle', 'srv'))

e.append(arr('a3', cx, 294, 0, 30, '#1e3a5f'))

# 4. Registry -> Tool Provider
e.append(box('reg', cx-90, 326, W, H, '#60a5fa', '#1e3a5f'))
e.append(txt('reg_t', cx-80, 334, 160, 34, 'Registry\nRoute to Tool', 12, '#ffffff', 'center', 'middle', 'reg'))

e.append(arr('a4', cx, 376, 0, 34, '#1e3a5f'))

# 5. DIAMOND: Which Tool?
e.append(diamond('d1', cx-100, 412, 200, 90, '#fef3c7', '#b45309'))
e.append(txt('d1_t', cx-55, 442, 110, 20, 'Tool Type?', 12, '#b45309', 'center', 'middle', 'd1'))

# ============================================================
# LEFT BRANCH: Search Flow (x ~ 80)
# ============================================================
lx = 100  # left column center

# Arrow left from diamond
e.append(arr2('al1', cx-100, 457, [[-130, 0]], '#047857'))
e.append(txt('al1l', cx-200, 440, 60, 14, 'search', 10, '#047857', 'center'))

# 6a. Query Validation
e.append(box('qv', lx-90, 432, W, 44, '#a7f3d0', '#047857'))
e.append(txt('qv_t', lx-80, 438, 160, 32, 'Query Validation\nisValidQuery()', 11, '#047857', 'center', 'middle', 'qv'))

e.append(arr('as1', lx, 476, 0, 28, '#047857'))

# 7a. DIAMOND: HyDE enabled?
e.append(diamond('d2', lx-80, 506, 160, 70, '#fef3c7', '#b45309'))
e.append(txt('d2_t', lx-42, 528, 84, 18, 'HyDE?', 10, '#b45309', 'center', 'middle', 'd2'))

# Yes path down
e.append(arr('as2y', lx, 576, 0, 22, '#b45309'))
e.append(txt('as2yl', lx+5, 577, 30, 14, 'Yes', 9, '#047857', 'left'))

# 8a. HyDE / MultiQuery
e.append(box('hyde', lx-90, 600, W, 44, '#fef3c7', '#b45309'))
e.append(txt('hyde_t', lx-80, 606, 160, 32, 'HyDE / MultiQuery\nTransform()', 11, '#b45309', 'center', 'middle', 'hyde'))

e.append(arr('as3', lx, 644, 0, 26, '#047857'))

# 9a. Embed Query
e.append(box('eq', lx-90, 672, W, 44, '#ddd6fe', '#6d28d9'))
e.append(txt('eq_t', lx-80, 678, 160, 32, 'Embed Query\nEmbedStrings()', 11, '#6d28d9', 'center', 'middle', 'eq'))

e.append(arr('as4', lx, 716, 0, 26, '#047857'))

# 10a. DIAMOND: Hybrid?
e.append(diamond('d3', lx-80, 744, 160, 70, '#93c5fd', '#1e3a5f'))
e.append(txt('d3_t', lx-38, 766, 76, 18, 'Hybrid?', 10, '#1e3a5f', 'center', 'middle', 'd3'))

# Left sub-branch: Vector only
e.append(arr2('ah1', lx-80, 779, [[-80, 0], [-80, 40]], '#1e3a5f'))
e.append(txt('ah1l', lx-200, 765, 40, 14, 'No', 9, '#dc2626', 'center'))

e.append(box('vs', lx-260, 820, 160, 44, '#3b82f6', '#1e3a5f'))
e.append(txt('vs_t', lx-250, 826, 140, 32, 'Vector Search\nFT.SEARCH KNN', 11, '#ffffff', 'center', 'middle', 'vs'))

# Right sub-branch: Keyword
e.append(arr2('ah2', lx+80, 779, [[80, 0], [80, 40]], '#1e3a5f'))
e.append(txt('ah2l', lx+85, 765, 40, 14, 'Yes', 9, '#047857', 'center'))

e.append(box('ks', lx+80, 820, 160, 44, '#93c5fd', '#1e3a5f'))
e.append(txt('ks_t', lx+90, 826, 140, 32, 'Keyword Search\nFT.SEARCH text', 11, '#1e3a5f', 'center', 'middle', 'ks'))

# Both merge -> RRF
e.append(arr2('am1', lx-180, 864, [[0, 30], [180, 30]], '#1e3a5f'))
e.append(arr2('am2', lx+160, 864, [[0, 30], [-160, 30]], '#1e3a5f'))

e.append(box('rrf', lx-90, 896, W, 44, '#93c5fd', '#1e3a5f'))
e.append(txt('rrf_t', lx-80, 902, 160, 32, 'RRF Merge\nmergeByRRF()', 11, '#1e3a5f', 'center', 'middle', 'rrf'))

e.append(arr('as5', lx, 940, 0, 26, '#047857'))

# 11a. DIAMOND: Rerank?
e.append(diamond('d4', lx-80, 968, 160, 70, '#ddd6fe', '#6d28d9'))
e.append(txt('d4_t', lx-38, 990, 76, 18, 'Rerank?', 10, '#6d28d9', 'center', 'middle', 'd4'))

e.append(arr('as6', lx, 1038, 0, 22, '#047857'))
e.append(txt('as6l', lx+5, 1036, 30, 14, 'Yes', 9, '#047857', 'left'))

# 12a. Reranker
e.append(box('rrk', lx-90, 1062, W, 44, '#ddd6fe', '#6d28d9'))
e.append(txt('rrk_t', lx-80, 1068, 160, 32, 'Reranker\nDashScope / Qwen3', 11, '#6d28d9', 'center', 'middle', 'rrk'))

e.append(arr('as7', lx, 1106, 0, 26, '#047857'))

# 13a. Context Compress
e.append(box('cc', lx-90, 1134, W, 44, '#fef3c7', '#b45309'))
e.append(txt('cc_t', lx-80, 1140, 160, 32, 'Context Compress\nCompress()', 11, '#b45309', 'center', 'middle', 'cc'))

e.append(arr('as8', lx, 1178, 0, 26, '#047857'))

# 14a. Return Results
e.append(box('rr', lx-90, 1206, W, 44, '#a7f3d0', '#047857'))
e.append(txt('rr_t', lx-80, 1212, 160, 32, 'Return Results\n[]RetrievalResult', 11, '#047857', 'center', 'middle', 'rr'))

# Arrow back to response
e.append(arr2('ar_back', lx+90, 1228, [[310+cx-lx-90, 0], [310+cx-lx-90, 70]], '#047857'))

# ============================================================
# RIGHT BRANCH: Index Flow (x ~ 720)
# ============================================================
rx = 720  # right column center

# Arrow right from diamond
e.append(arr2('ar1', cx+100, 457, [[120, 0]], '#dc2626'))
e.append(txt('ar1l', cx+110, 440, 100, 14, 'index / upload', 10, '#dc2626', 'center'))

# 6b. Parse Document
e.append(box('pd', rx-90, 432, W, 44, '#93c5fd', '#1e3a5f'))
e.append(txt('pd_t', rx-80, 438, 160, 32, 'Parse Document\nMD/HTML/PDF/DOCX', 11, '#1e3a5f', 'center', 'middle', 'pd'))

e.append(arr('ai1', rx, 476, 0, 28, '#dc2626'))

# 7b. Chunk Text
e.append(box('ct', rx-90, 506, W, 44, '#93c5fd', '#1e3a5f'))
e.append(txt('ct_t', rx-80, 512, 160, 32, 'Chunk Text\nStructure/Semantic/Code', 11, '#1e3a5f', 'center', 'middle', 'ct'))

e.append(arr('ai2', rx, 550, 0, 28, '#dc2626'))

# 8b. DIAMOND: Async?
e.append(diamond('d5', rx-80, 580, 160, 70, '#fee2e2', '#dc2626'))
e.append(txt('d5_t', rx-38, 602, 76, 18, 'Async?', 10, '#dc2626', 'center', 'middle', 'd5'))

# Yes -> Queue
e.append(arr2('ai3y', rx+80, 615, [[100, 0]], '#dc2626'))
e.append(txt('ai3yl', rx+85, 600, 30, 14, 'Yes', 9, '#dc2626', 'left'))

e.append(box('wq', rx+180, 590, 160, 50, '#fee2e2', '#dc2626'))
e.append(txt('wq_t', rx+190, 596, 140, 38, 'Redis Queue\nWorker Pool\n\u5f02\u6b65\u6267\u884c', 10, '#dc2626', 'center', 'middle', 'wq'))

# No -> direct path
e.append(arr('ai3n', rx, 650, 0, 22, '#dc2626'))
e.append(txt('ai3nl', rx+5, 648, 30, 14, 'No', 9, '#64748b', 'left'))

# 9b. Embed Chunks
e.append(box('ec', rx-90, 674, W, 44, '#ddd6fe', '#6d28d9'))
e.append(txt('ec_t', rx-80, 680, 160, 32, 'Embed Chunks\nEmbedStrings()', 11, '#6d28d9', 'center', 'middle', 'ec'))

e.append(arr('ai4', rx, 718, 0, 28, '#dc2626'))

# 10b. DIAMOND: Cache hit?
e.append(diamond('d6', rx-80, 748, 160, 70, '#a7f3d0', '#047857'))
e.append(txt('d6_t', rx-38, 770, 76, 18, 'Cache?', 10, '#047857', 'center', 'middle', 'd6'))

# Hit -> skip API
e.append(arr2('ai5h', rx+80, 783, [[100, 0]], '#047857'))
e.append(txt('ai5hl', rx+85, 768, 50, 14, 'L1/L2 Hit', 9, '#047857', 'left'))
e.append(box('ch', rx+180, 758, 140, 50, '#a7f3d0', '#047857', 1))
e.append(txt('ch_t', rx+190, 764, 120, 38, 'Cache Return\nLRU / Redis', 10, '#047857', 'center', 'middle', 'ch'))

# Miss -> call API
e.append(arr('ai5m', rx, 818, 0, 22, '#dc2626'))
e.append(txt('ai5ml', rx+5, 816, 40, 14, 'Miss', 9, '#dc2626', 'left'))

# 11b. Call Embedding API
e.append(box('ea', rx-90, 842, W, 44, '#fef3c7', '#b45309'))
e.append(txt('ea_t', rx-80, 848, 160, 32, 'Embedding API\nOpenAI / Ark / Local', 11, '#b45309', 'center', 'middle', 'ea'))

e.append(arr('ai6', rx, 886, 0, 28, '#dc2626'))

# 12b. Upsert to VectorStore
e.append(box('up', rx-90, 916, W, 44, '#3b82f6', '#1e3a5f'))
e.append(txt('up_t', rx-80, 922, 160, 32, 'Upsert VectorStore\nRedis / Milvus / Qdrant', 11, '#ffffff', 'center', 'middle', 'up'))

e.append(arr('ai7', rx, 960, 0, 28, '#dc2626'))

# 13b. DIAMOND: Graph RAG?
e.append(diamond('d7', rx-80, 990, 160, 70, '#ddd6fe', '#6d28d9'))
e.append(txt('d7_t', rx-38, 1012, 76, 18, 'Graph?', 10, '#6d28d9', 'center', 'middle', 'd7'))

e.append(arr2('ai8y', rx+80, 1025, [[100, 0]], '#6d28d9'))
e.append(txt('ai8yl', rx+85, 1010, 30, 14, 'Yes', 9, '#6d28d9', 'left'))

e.append(box('ge', rx+180, 1000, 160, 50, '#ddd6fe', '#6d28d9', 1))
e.append(txt('ge_t', rx+190, 1006, 140, 38, 'Graph Extract\nEntities \u2192 Neo4j', 10, '#6d28d9', 'center', 'middle', 'ge'))

e.append(arr('ai8n', rx, 1060, 0, 22, '#dc2626'))

# 14b. Index Complete
e.append(box('ic', rx-90, 1084, W, 44, '#a7f3d0', '#047857'))
e.append(txt('ic_t', rx-80, 1090, 160, 32, 'Index Complete\nReturn fileID', 11, '#047857', 'center', 'middle', 'ic'))

# Arrow to response
e.append(arr2('ai_back', rx-90, 1106, [[-220+cx-rx+90, 0], [-220+cx-rx+90, 192]], '#047857'))

# ============================================================
# GRAPH SEARCH BRANCH (far right, smaller)
# ============================================================
gx = 960

# Arrow far right from diamond
e.append(arr2('ag1', cx+100, 435, [[452, 0], [452, 30]], '#6d28d9'))
e.append(txt('ag1l', cx+380, 418, 100, 14, 'graph_search', 10, '#6d28d9', 'center'))

e.append(box('gs1', gx-80, 467, 160, 44, '#ddd6fe', '#6d28d9'))
e.append(txt('gs1_t', gx-70, 473, 140, 32, 'Graph Query\nNeo4j Cypher', 11, '#6d28d9', 'center', 'middle', 'gs1'))

e.append(arr('ag2', gx, 511, 0, 26, '#6d28d9'))

e.append(box('gs2', gx-80, 539, 160, 44, '#ddd6fe', '#6d28d9'))
e.append(txt('gs2_t', gx-70, 545, 140, 32, 'Entity + Relations\nSubgraph Walk', 11, '#6d28d9', 'center', 'middle', 'gs2'))

e.append(arr('ag3', gx, 583, 0, 26, '#6d28d9'))

e.append(box('gs3', gx-80, 611, 160, 44, '#a7f3d0', '#047857'))
e.append(txt('gs3_t', gx-70, 617, 140, 32, 'Return Graph\nResults', 11, '#047857', 'center', 'middle', 'gs3'))

e.append(arr2('ag_back', gx-80, 633, [[-552+cx-gx+80, 0], [-552+cx-gx+80, 665]], '#047857'))

# ============================================================
# END: Response to Client
# ============================================================
e.append(box('resp', cx-100, 1300, 200, 50, '#3b82f6', '#1e3a5f', 3))
e.append(txt('resp_t', cx-90, 1308, 180, 34, 'MCP Response\nJSON-RPC \u2192 Client', 12, '#ffffff', 'center', 'middle', 'resp'))

e.append(arr('aend', cx, 1350, 0, 30, '#1e3a5f'))

e.append(ell('end', cx-60, 1382, 120, 46, '#fed7aa', '#c2410c'))
e.append(txt('end_t', cx-45, 1393, 90, 22, 'Client', 13, '#c2410c', 'center', 'middle', 'end'))

# ============================================================
# SIDE ANNOTATIONS: Config + Embedding Manager detail
# ============================================================

# Config annotation (top right)
e.append(box('cfg', 1060, 80, 180, 70, '#fef3c7', '#b45309', 1, 'dashed'))
e.append(txt('cfg_t', 1070, 88, 160, 54, 'config.toml\n\u2193\nServerConfig\n\u2192 To*Config()', 10, '#b45309', 'center', 'middle', 'cfg'))
e.append(arr2('cfg_a', 1060, 115, [[-570+cx-490, 0]], '#b45309', 1, 'dashed'))

# Embedding Manager detail box (bottom right)
e.append(box('embox', 1050, 620, 220, 200, '#f8fafc', '#94a3b8', 1, 'dashed'))
e.append(txt('embox_t', 1060, 628, 200, 16, 'Embedding Manager \u5185\u90e8', 11, '#1e40af', 'left'))

e.append(box('ep1', 1065, 650, 190, 30, '#ddd6fe', '#6d28d9', 1))
e.append(txt('ep1t', 1070, 654, 180, 22, 'Provider A: OpenAI/Ark', 9, '#6d28d9', 'center', 'middle', 'ep1'))

e.append(box('ep2', 1065, 685, 190, 30, '#ddd6fe', '#6d28d9', 1))
e.append(txt('ep2t', 1070, 689, 180, 22, 'Provider B: DashScope', 9, '#6d28d9', 'center', 'middle', 'ep2'))

e.append(box('ep3', 1065, 720, 190, 30, '#ddd6fe', '#6d28d9', 1))
e.append(txt('ep3t', 1070, 724, 180, 22, 'Provider C: Local Model', 9, '#6d28d9', 'center', 'middle', 'ep3'))

e.append(box('cb', 1065, 760, 90, 48, '#fee2e2', '#dc2626', 1))
e.append(txt('cbt', 1070, 764, 80, 40, '\u7194\u65ad\u5668\nCircuit\nBreaker', 8, '#dc2626', 'center', 'middle', 'cb'))

e.append(box('lb', 1165, 760, 90, 48, '#fef3c7', '#b45309', 1))
e.append(txt('lbt', 1170, 764, 80, 40, '\u8d1f\u8f7d\u5747\u8861\nLoad\nBalance', 8, '#b45309', 'center', 'middle', 'lb'))

# Link from Embed boxes
e.append(arr2('em_link1', lx+90, 690, [[960-lx-90, 0]], '#94a3b8', 1, 'dashed'))
e.append(arr2('em_link2', rx+90, 696, [[1050-rx-90, 0]], '#94a3b8', 1, 'dashed'))

# ============================================================
# LEGEND
# ============================================================
e.append(box('leg', 1050, 860, 220, 130, '#f8fafc', '#94a3b8', 1, 'dashed'))
e.append(txt('leg_t', 1060, 868, 200, 16, '\u56fe\u4f8b', 12, '#374151', 'left'))
e.append(box('lg1', 1065, 890, 24, 16, '#3b82f6', '#1e3a5f', 1))
e.append(txt('lg1t', 1095, 890, 160, 16, '\u6838\u5fc3\u670d\u52a1 / \u5b58\u50a8', 10, '#374151', 'left'))
e.append(box('lg2', 1065, 912, 24, 16, '#ddd6fe', '#6d28d9', 1))
e.append(txt('lg2t', 1095, 912, 160, 16, 'Embedding / Rerank', 10, '#374151', 'left'))
e.append(box('lg3', 1065, 934, 24, 16, '#fef3c7', '#b45309', 1))
e.append(txt('lg3t', 1095, 934, 160, 16, '\u53ef\u9009\u589e\u5f3a / API\u8c03\u7528', 10, '#374151', 'left'))
e.append(box('lg4', 1065, 956, 24, 16, '#a7f3d0', '#047857', 1))
e.append(txt('lg4t', 1095, 956, 160, 16, '\u8fd4\u56de\u7ed3\u679c / \u7f13\u5b58', 10, '#374151', 'left'))

# Diamond in legend
e.append(diamond('lg5', 1061, 976, 32, 20, '#fef3c7', '#b45309'))
e.append(txt('lg5t', 1095, 978, 160, 16, '\u51b3\u7b56\u5206\u652f', 10, '#374151', 'left'))

# ============================================================
# FLOW LABELS on branches
# ============================================================
e.append(txt('fl_s', lx-90, 415, 180, 14, '\u2460 \u68c0\u7d22\u6d41\u7a0b (Search)', 11, '#047857', 'center'))
e.append(txt('fl_i', rx-90, 415, 180, 14, '\u2461 \u7d22\u5f15\u6d41\u7a0b (Index)', 11, '#dc2626', 'center'))
e.append(txt('fl_g', gx-80, 450, 160, 14, '\u2462 \u56fe\u68c0\u7d22 (Graph)', 11, '#6d28d9', 'center'))

# No-skip arrows for diamonds (bypass path)
# d2 No -> skip HyDE
e.append(arr2('d2n', lx-80, 541, [[-50, 0], [-50, 131], [80, 131]], '#64748b', 1, 'dashed'))
e.append(txt('d2nl', lx-165, 528, 30, 14, 'No', 9, '#dc2626', 'center'))

# d4 No -> skip Rerank
e.append(arr2('d4n', lx-80, 1003, [[-50, 0], [-50, 131], [80, 131]], '#64748b', 1, 'dashed'))
e.append(txt('d4nl', lx-165, 990, 30, 14, 'No', 9, '#dc2626', 'center'))

doc = {'type':'excalidraw','version':2,'source':'https://excalidraw.com',
    'elements':e,'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}

with open('rag-architecture-detail.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc,f,ensure_ascii=False,indent=2)

print('OK! elements=%d' % len(e))
