# -*- coding: utf-8 -*-
import json

elements = []
seed = 100000

def s():
    global seed; seed += 1; return seed

def txt(id,x,y,w,h,t,sz=16,c='#374151',a='center',va='top',cid=None):
    return {'type':'text','id':id,'x':x,'y':y,'width':w,'height':h,'text':t,'originalText':t,
        'fontSize':sz,'fontFamily':3,'textAlign':a,'verticalAlign':va,'strokeColor':c,
        'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':1,'strokeStyle':'solid',
        'roughness':0,'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),
        'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,
        'containerId':cid,'lineHeight':1.25}

def box(id,x,y,w,h,f,st,sw=2,ss='solid',be=None):
    return {'type':'rectangle','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':st,
        'backgroundColor':f,'fillStyle':'solid','strokeWidth':sw,'strokeStyle':ss,'roughness':0,
        'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,
        'groupIds':[],'boundElements':be or [],'link':None,'locked':False,'roundness':{'type':3}}

def ell(id,x,y,w,h,f,st,be=None):
    return {'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':st,
        'backgroundColor':f,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,
        'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,
        'groupIds':[],'boundElements':be or [],'link':None,'locked':False}

def arr(id,x,y,pts,st,sw=2,ss='solid',ea='arrow'):
    return {'type':'arrow','id':id,'x':x,'y':y,'width':max(abs(p[0]) for p in pts),
        'height':max(abs(p[1]) for p in pts),'strokeColor':st,'backgroundColor':'transparent',
        'fillStyle':'solid','strokeWidth':sw,'strokeStyle':ss,'roughness':0,'opacity':100,'angle':0,
        'seed':s(),'version':1,'versionNonce':s(),'isDeleted':False,'groupIds':[],
        'boundElements':None,'link':None,'locked':False,'points':pts,
        'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':ea}

def ln(id,x,y,pts,st,sw=1,ss='dashed'):
    return {'type':'line','id':id,'x':x,'y':y,'width':0,'height':0,'strokeColor':st,
        'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':ss,
        'roughness':0,'opacity':100,'angle':0,'seed':s(),'version':1,'versionNonce':s(),
        'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'points':pts}

e = elements
# TITLE
e.append(txt('title',380,15,800,40,'RAG MCP Server \u2014 \u7cfb\u7edf\u67b6\u6784\u5168\u666f\u56fe',28,'#1e40af'))
e.append(txt('sub',380,55,800,18,'Streamable HTTP \xb7 Multi-Provider Embedding \xb7 Hybrid Retrieval \xb7 Graph RAG',12,'#64748b'))

# LAYER LABELS + DIVIDERS
e.append(txt('l1',30,85,100,16,'Transport',11,'#3b82f6','left'))
e.append(ln('d1',30,100,[[0,0],[1520,0]],'#cbd5e1'))
e.append(txt('l2',30,275,100,16,'Application',11,'#3b82f6','left'))
e.append(ln('d2',30,290,[[0,0],[1520,0]],'#cbd5e1'))
e.append(txt('l3',30,510,140,16,'Domain (RAG Core)',11,'#3b82f6','left'))
e.append(ln('d3',30,525,[[0,0],[1520,0]],'#cbd5e1'))
e.append(txt('l4',30,850,100,16,'Infrastructure',11,'#3b82f6','left'))
e.append(ln('d4',30,865,[[0,0],[1520,0]],'#cbd5e1'))

# === TRANSPORT ===
e.append(ell('cli',60,120,140,60,'#fed7aa','#c2410c'))
e.append(txt('cli_t',85,140,90,20,'MCP Client',13,'#c2410c','center','middle','cli'))
e.append(arr('a1',202,150,[[0,0],[85,0]],'#c2410c'))
e.append(txt('a1l',215,132,60,14,'HTTP POST',9,'#64748b'))

e.append(box('http',290,115,175,75,'#3b82f6','#1e3a5f'))
e.append(txt('http_t',300,130,155,45,'Streamable HTTP\n/mcp  /upload',13,'#ffffff','center','middle','http'))
e.append(txt('http_c',310,195,130,14,'server.go',9,'#64748b'))
e.append(arr('a2',467,152,[[0,0],[70,0]],'#1e3a5f'))

e.append(box('srv',540,110,210,90,'#3b82f6','#1e3a5f',3))
e.append(txt('srv_t',560,128,170,55,'MCP Server\nmcp-go/server',16,'#ffffff','center','middle','srv'))
e.append(txt('srv_c',575,205,140,14,'main.go  server.go',9,'#64748b'))
e.append(arr('a3',752,155,[[0,0],[75,0]],'#1e3a5f'))

e.append(box('reg',830,120,170,70,'#60a5fa','#1e3a5f'))
e.append(txt('reg_t',840,132,150,45,'Registry\nApplyToServer()',13,'#ffffff','center','middle','reg'))
e.append(txt('reg_c',845,195,140,14,'tools/registry.go',9,'#64748b'))

e.append(box('cfg',1100,110,200,90,'#fef3c7','#b45309'))
e.append(txt('cfg_t',1110,120,180,70,'ServerConfig\nTOML \u2192 To*Config()\n\u2192 \u9886\u57df\u914d\u7f6e',12,'#b45309','center','middle','cfg'))
e.append(txt('cfg_c',1120,205,160,14,'config.go  config.toml',9,'#64748b'))
e.append(arr('ac',1098,155,[[-265,0]],'#b45309',1,'dashed'))

# === APPLICATION ===
e.append(box('tp',80,305,210,60,'#ddd6fe','#6d28d9'))
e.append(txt('tp_t',90,315,190,40,'RAGToolProvider\n12 MCP Tools',13,'#6d28d9','center','middle','tp'))
e.append(txt('tp_c',100,370,170,14,'tools/rag_tools.go',9,'#64748b'))

tools = [
    ('t1',360,300,'rag_search'),('t2',510,300,'rag_index_document'),
    ('t3',690,300,'rag_index_url'),('t4',850,300,'rag_build_prompt'),
    ('t5',360,350,'rag_chunk_text'),('t6',510,350,'rag_status'),
    ('t7',690,350,'rag_delete_document'),('t8',850,350,'rag_parse_document'),
    ('t9',360,400,'rag_task_status'),('t10',510,400,'rag_graph_search'),
    ('t11',690,400,'rag_list_documents'),('t12',850,400,'rag_export_data'),
]
for tid,tx,ty,tn in tools:
    e.append(box(tid,tx,ty,130,38,'#f1f5f9','#94a3b8',1))
    e.append(txt(tid+'t',tx+5,ty+5,120,28,tn,10,'#374151','center','middle',tid))

e.append(arr('af1',292,335,[[66,-10]],'#6d28d9',1))
e.append(arr('af2',292,335,[[66,-35]],'#6d28d9',1))
e.append(arr('af3',292,335,[[66,25]],'#6d28d9',1))
e.append(arr('af4',292,335,[[66,65]],'#6d28d9',1))

e.append(box('pr',1060,305,175,55,'#f1f5f9','#94a3b8',1))
e.append(txt('pr_t',1070,312,155,40,'Prompts & Resources',11,'#374151','center','middle','pr'))
e.append(txt('pr_c',1070,365,160,28,'tools/prompts.go\ntools/resources.go',9,'#64748b','left'))

# === DOMAIN ===
e.append(box('ret',60,540,250,100,'#3b82f6','#1e3a5f',3))
e.append(txt('ret_t',75,555,220,70,'MultiFileRetriever\nSearch \xb7 Index \xb7 Delete\nHybrid \xb7 Collection',13,'#ffffff','center','middle','ret'))
e.append(txt('ret_c',70,645,230,28,'rag/retriever.go\nrag/retriever_adapter.go',9,'#64748b','left'))

e.append(box('chk',360,540,185,80,'#93c5fd','#1e3a5f'))
e.append(txt('chk_t',370,550,165,60,'Chunking Engine\nStructure \xb7 Semantic\nCode \xb7 Parent-Child',11,'#1e3a5f','center','middle','chk'))
e.append(txt('chk_c',365,625,175,42,'rag/chunking.go\nrag/chunking_semantic.go\nrag/chunking_code.go',9,'#64748b','left'))

e.append(box('emb',595,540,205,80,'#ddd6fe','#6d28d9'))
e.append(txt('emb_t',605,550,185,60,'Embedding Manager\nMulti-Provider\n\u7194\u65ad/\u8d1f\u8f7d\u5747\u8861/\u5065\u5eb7\u68c0\u67e5',11,'#6d28d9','center','middle','emb'))
e.append(txt('emb_c',600,625,200,28,'rag/embedding_manager.go\nrag/embedding_provider.go',9,'#64748b','left'))

e.append(box('cache',850,540,165,80,'#a7f3d0','#047857'))
e.append(txt('cache_t',860,550,145,60,'Embedding Cache\nL1: LRU (local)\nL2: Redis',11,'#047857','center','middle','cache'))
e.append(txt('cache_c',855,625,155,14,'rag/embedding_cache.go',9,'#64748b','left'))

e.append(box('parse',360,690,185,70,'#93c5fd','#1e3a5f'))
e.append(txt('parse_t',370,698,165,55,'Parsing Engine\nMD \xb7 HTML \xb7 PDF \xb7 DOCX',11,'#1e3a5f','center','middle','parse'))
e.append(txt('parse_c',365,765,175,28,'rag/parsing_engine.go\nrag/parsing_pdf.go',9,'#64748b','left'))

e.append(box('rrk',595,690,165,70,'#ddd6fe','#6d28d9'))
e.append(txt('rrk_t',605,698,145,55,'Reranker\nDashScope \xb7 Generic\nQwen3 \xb7 GTE',11,'#6d28d9','center','middle','rrk'))
e.append(txt('rrk_c',600,765,160,14,'rag/retriever_reranker.go',9,'#64748b','left'))

e.append(box('qe',810,690,165,70,'#fef3c7','#b45309'))
e.append(txt('qe_t',820,698,145,55,'Query Enhancement\nHyDE \xb7 MultiQuery\nCompressor',11,'#b45309','center','middle','qe'))
e.append(txt('qe_c',815,765,160,28,'rag/retriever_hyde.go\nrag/retriever_multiquery.go',9,'#64748b','left'))

e.append(box('grag',1060,540,185,80,'#ddd6fe','#6d28d9'))
e.append(txt('grag_t',1070,550,165,60,'Graph RAG\nEntity Extraction\nNeo4j / InMemory',11,'#6d28d9','center','middle','grag'))
e.append(txt('grag_c',1065,625,175,28,'rag/graph_rag.go\nrag/graph_extractor.go',9,'#64748b','left'))

e.append(box('async',1060,690,185,70,'#fee2e2','#dc2626'))
e.append(txt('async_t',1070,698,165,55,'Async Index\nRedis Streams Queue\nWorker Pool',11,'#dc2626','center','middle','async'))
e.append(txt('async_c',1065,765,175,28,'rag/worker_queue.go\nrag/worker_pool.go',9,'#64748b','left'))

e.append(box('upl',1300,540,155,70,'#a7f3d0','#047857',1))
e.append(txt('upl_t',1310,550,135,50,'Upload Store\nDisk + Redis TTL',10,'#047857','center','middle','upl'))
e.append(txt('upl_c',1305,615,145,14,'rag/upload_store.go',9,'#64748b','left'))

e.append(box('mig',1300,690,155,70,'#f1f5f9','#94a3b8',1))
e.append(txt('mig_t',1310,698,135,55,'Schema Migration\nVersion Check',10,'#374151','center','middle','mig'))
e.append(txt('mig_c',1305,765,145,14,'rag/store_migration.go',9,'#64748b','left'))

# domain arrows
e.append(arr('da1',312,580,[[46,0]],'#1e3a5f',1))
e.append(arr('da2',547,580,[[46,0]],'#1e3a5f',1))
e.append(arr('da3',802,580,[[46,0]],'#1e3a5f',1))
e.append(arr('da4',185,642,[[0,48],[175,48]],'#1e3a5f',1,'dashed'))
e.append(arr('da5',185,642,[[0,83],[410,83]],'#1e3a5f',1,'dashed'))
e.append(arr('da6',185,642,[[0,83],[625,83]],'#1e3a5f',1,'dashed'))

# === INFRASTRUCTURE ===
e.append(txt('vsi',150,830,700,16,'VectorStore interface { EnsureIndex \xb7 UpsertVectors \xb7 SearchVectors \xb7 HybridSearch \xb7 DeleteByFileID }',10,'#1e3a5f','left'))

e.append(box('redis',120,880,210,80,'#3b82f6','#1e3a5f'))
e.append(txt('redis_t',135,890,180,60,'Redis (VectorStore)\nRediSearch FT.SEARCH\nStandalone/Sentinel/Cluster',10,'#ffffff','center','middle','redis'))
e.append(txt('redis_c',140,965,170,14,'rag/store_redis.go',9,'#64748b'))

e.append(box('milvus',400,880,175,80,'#93c5fd','#1e3a5f'))
e.append(txt('milvus_t',415,892,145,55,'Milvus\nVector Database\nREST API',10,'#1e3a5f','center','middle','milvus'))
e.append(txt('milvus_c',415,965,145,14,'rag/store_milvus.go',9,'#64748b'))

e.append(box('qdrant',650,880,175,80,'#93c5fd','#1e3a5f'))
e.append(txt('qdrant_t',665,892,145,55,'Qdrant\nVector Database\nREST API',10,'#1e3a5f','center','middle','qdrant'))
e.append(txt('qdrant_c',665,965,145,14,'rag/store_qdrant.go',9,'#64748b'))

e.append(box('neo4j',910,880,175,80,'#ddd6fe','#6d28d9'))
e.append(txt('neo4j_t',925,892,145,55,'Neo4j\nGraph Database\nCypher Queries',10,'#6d28d9','center','middle','neo4j'))
e.append(txt('neo4j_c',925,965,145,14,'rag/graph_neo4j.go',9,'#64748b'))

e.append(box('eapi',1170,880,195,80,'#fef3c7','#b45309'))
e.append(txt('eapi_t',1185,890,165,60,'Embedding APIs\nOpenAI \xb7 Ark \xb7 Local\nDashScope Rerank',10,'#b45309','center','middle','eapi'))

# infra arrows
e.append(arr('ia1',225,820,[[0,58]],'#1e3a5f',1))
e.append(arr('ia2',487,810,[[0,68]],'#1e3a5f',1,'dashed'))
e.append(arr('ia3',737,810,[[0,68]],'#1e3a5f',1,'dashed'))
e.append(arr('ia4',1152,625,[[0,253]],'#6d28d9',1,'dashed'))
e.append(arr('ia5',700,625,[[0,60],[567,250]],'#b45309',1,'dashed'))

# === RETRIEVER PIPELINE DETAIL ===
e.append(box('exp1',40,1000,1420,165,'#f8fafc','#94a3b8',1,'dashed'))
e.append(txt('exp1_t',55,1008,450,16,'\u2460 Retriever Pipeline \u2014 \u68c0\u7d22\u6d41\u7a0b\u5c55\u5f00',13,'#1e40af','left'))

steps = [
    ('ps1',60,1040,'Query\nValidation','#fed7aa','#c2410c'),
    ('ps2',195,1040,'HyDE /\nMultiQuery','#fef3c7','#b45309'),
    ('ps3',330,1040,'Embed\nQuery','#ddd6fe','#6d28d9'),
    ('ps4',465,1040,'Vector\nSearch','#3b82f6','#1e3a5f'),
    ('ps5',600,1040,'Keyword\nBM25','#93c5fd','#1e3a5f'),
    ('ps6',735,1040,'RRF\nMerge','#93c5fd','#1e3a5f'),
    ('ps7',870,1040,'Rerank\nTop-N','#ddd6fe','#6d28d9'),
    ('ps8',1005,1040,'Context\nCompress','#fef3c7','#b45309'),
    ('ps9',1140,1040,'Return\nResults','#a7f3d0','#047857'),
]
for sid,sx,sy,st2,sf,ss2 in steps:
    e.append(box(sid,sx,sy,115,55,sf,ss2,1))
    e.append(txt(sid+'t',sx+5,sy+5,105,45,st2,11,ss2,'center','middle',sid))

for i in range(len(steps)-1):
    sx2=steps[i][1]+117; sy2=steps[i][2]+27
    e.append(arr('pa'+str(i),sx2,sy2,[[16,0]],'#1e3a5f',1))

labels = ['isValidQuery()','Transform()','EmbedStrings()','FT.SEARCH','FT.SEARCH txt','mergeByRRF()','Rerank() API','Compress()','[]RetrievalResult']
for i,lb in enumerate(labels):
    e.append(txt('pl'+str(i),steps[i][1],1100,115,14,lb,8,'#64748b'))

e.append(txt('opt1',203,1028,80,12,'\u53ef\u9009',8,'#b45309'))
e.append(txt('opt2',878,1028,80,12,'\u53ef\u9009',8,'#6d28d9'))
e.append(txt('opt3',1013,1028,80,12,'\u53ef\u9009',8,'#b45309'))
e.append(txt('opt4',608,1028,80,12,'\u6df7\u5408',8,'#1e3a5f'))

e.append(arr('el1',185,675,[[0,323]],'#94a3b8',1,'dashed'))

# === EMBEDDING MANAGER DETAIL ===
e.append(box('exp2',40,1185,1420,125,'#f8fafc','#94a3b8',1,'dashed'))
e.append(txt('exp2_t',55,1193,500,16,'\u2461 Embedding Manager \u2014 \u591a Provider \u5f39\u6027\u67b6\u6784',13,'#1e40af','left'))

provs = [('ep1',70,1225,'Provider A\nOpenAI / Ark'),('ep2',265,1225,'Provider B\nDashScope'),('ep3',460,1225,'Provider C\nLocal Model')]
for pid,px,py,pt2 in provs:
    e.append(box(pid,px,py,155,50,'#ddd6fe','#6d28d9',1))
    e.append(txt(pid+'t',px+5,py+5,145,40,pt2,10,'#6d28d9','center','middle',pid))

feats = [
    ('ef1',690,1220,'\u7194\u65ad\u5668 Circuit Breaker\nClosed \u2192 Open \u2192 HalfOpen','#fee2e2','#dc2626'),
    ('ef2',910,1220,'\u8d1f\u8f7d\u5747\u8861 LoadBalance\nPriority / Weighted / Random','#fef3c7','#b45309'),
    ('ef3',1170,1220,'\u5065\u5eb7\u68c0\u67e5 HealthCheck\n\u5b9a\u65f6Ping + \u6307\u6570\u9000\u907f\u91cd\u8bd5','#a7f3d0','#047857'),
]
for fid,fx,fy,ft2,ff2,fss in feats:
    e.append(box(fid,fx,fy,185,60,ff2,fss,1))
    e.append(txt(fid+'t',fx+5,fy+5,175,50,ft2,10,fss,'center','middle',fid))

e.append(arr('el2',697,625,[[0,558]],'#94a3b8',1,'dashed'))

doc = {'type':'excalidraw','version':2,'source':'https://excalidraw.com',
    'elements':e,'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}

with open('rag-architecture-detail.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc,f,ensure_ascii=False,indent=2)

print('OK! elements=%d' % len(e))
