# -*- coding: utf-8 -*-
"""Generate detailed Index Document Pipeline flowchart."""
import json
elements=[];seed=700000
def ns():
    global seed;seed+=1;return seed
def add_text(id,x,y,w,h,text,sz=12,color='#374151',align='center',valign='middle',cid=None):
    elements.append({'type':'text','id':id,'x':x,'y':y,'width':w,'height':h,'text':text,'originalText':text,'fontSize':sz,'fontFamily':3,'textAlign':align,'verticalAlign':valign,'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':1,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'containerId':cid,'lineHeight':1.25})
def add_rect(id,x,y,w,h,fill,stroke,sw=2):
    elements.append({'type':'rectangle','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':sw,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False,'roundness':{'type':3}});return(x,y,w,h)
def add_diamond(id,x,y,w,h,fill,stroke):
    elements.append({'type':'diamond','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False});return(x,y,w,h)
def add_ellipse(id,x,y,w,h,fill,stroke):
    elements.append({'type':'ellipse','id':id,'x':x,'y':y,'width':w,'height':h,'strokeColor':stroke,'backgroundColor':fill,'fillStyle':'solid','strokeWidth':2,'strokeStyle':'solid','roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':[],'link':None,'locked':False});return(x,y,w,h)
def add_arrow(id,x1,y1,x2,y2,color,sw=2,style='solid'):
    dx=x2-x1;dy=y2-y1;elements.append({'type':'arrow','id':id,'x':x1,'y':y1,'width':abs(dx),'height':abs(dy),'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'points':[[0,0],[dx,dy]],'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})
def add_arrow_path(id,pts,color,sw=2,style='solid'):
    x0,y0=pts[0];rel=[[px-x0,py-y0] for px,py in pts];w=max(abs(p[0]) for p in rel) if rel else 0;h=max(abs(p[1]) for p in rel) if rel else 0;elements.append({'type':'arrow','id':id,'x':x0,'y':y0,'width':w,'height':h,'strokeColor':color,'backgroundColor':'transparent','fillStyle':'solid','strokeWidth':sw,'strokeStyle':style,'roughness':0,'opacity':100,'angle':0,'seed':ns(),'version':1,'versionNonce':ns(),'isDeleted':False,'groupIds':[],'boundElements':None,'link':None,'locked':False,'points':rel,'startBinding':None,'endBinding':None,'startArrowhead':None,'endArrowhead':'arrow'})
def bot(b):return(b[0]+b[2]/2,b[1]+b[3])
def top(b):return(b[0]+b[2]/2,b[1])
def lft(b):return(b[0],b[1]+b[3]/2)
def rgt(b):return(b[0]+b[2],b[1]+b[3]/2)

C_BLUE='#3b82f6';S_BLUE='#1e3a5f';C_GREEN='#a7f3d0';S_GREEN='#047857';C_YELLOW='#fef3c7';S_YELLOW='#b45309';C_PURPLE='#ddd6fe';S_PURPLE='#6d28d9';C_RED='#fee2e2';S_RED='#dc2626';C_GRAY='#f1f5f9';S_GRAY='#64748b'
GAP=55;W=240;H=56;DW=180;DH=80;NW=280;cx=400;nx=cx+W/2+40

add_text('title',80,15,640,28,'IndexDocument — 文档索引管线详细流程',22,'#1e40af')
add_text('sub',80,48,640,15,'Delete旧数据 → 智能分块 → 缓存去重 → 分批Embed → EnsureIndex → Pipeline Upsert',9,S_GRAY)
y=80

# START
n0=add_ellipse('start',cx-75,y,150,40,'#fed7aa','#c2410c')
add_text('s0t',cx-60,y+8,120,24,'IndexDocument()',12,'#c2410c','center','middle','start')
add_text('s0f',cx-75,y+44,150,12,'retriever.go:IndexDocument()',9,S_GRAY)
add_arrow('a01',*bot(n0),cx,y+40+GAP,S_RED)
y+=40+GAP

# 1. Upsert删旧
n1=add_rect('del',cx-W/2,y,W,H,C_RED,S_RED)
add_text('del_t',cx-W/2+5,y+4,W-10,H-8,'Delete Old Chunks\nDeleteByFileID(indexName,fileID)',10,S_RED,'center','middle','del')
add_rect('del_n',nx,y-6,NW,56,C_GRAY,S_GRAY,1)
add_text('del_nt',nx+8,y,NW-16,44,'说明: Upsert语义\n• 先删旧chunk再写新chunk\n• 避免重复索引产生残留\n• FT.SEARCH file_id→DEL逐一删除\n• 删除失败不阻止索引(可能首次)',9,'#374151','left','top','del_n')
add_arrow('a02',*bot(n1),cx,y+H+GAP,S_GREEN)
y+=H+GAP

# 2. Smart Chunking Decision
d1=add_diamond('d_ck',cx-DW/2,y,DW,DH,C_YELLOW,S_YELLOW)
add_text('d_ck_t',cx-40,y+DH/2-8,80,16,'文件类型?',10,S_YELLOW,'center','middle','d_ck')

# 4 branches: Code / Semantic / Structure / Fixed
bw=160;bh=50
# Code (far left)
code_x=20;code_y=y+DH+GAP
add_arrow_path('a_code',[lft(d1),(code_x+bw/2,y+DH/2),(code_x+bw/2,code_y)],S_PURPLE)
add_text('a_code_l',code_x+bw/2-20,y+DH/2-16,40,12,'Code',8,S_PURPLE)
n_code=add_rect('code',code_x,code_y,bw,bh,C_PURPLE,S_PURPLE)
add_text('code_t',code_x+5,code_y+4,bw-10,bh-8,'代码感知分块\nCodeChunking()',9,S_PURPLE,'center','middle','code')
add_text('code_f',code_x,code_y+bh+2,bw,12,'chunking_code.go',8,S_GRAY,'left')

# Semantic (center-left)
sem_x=200;sem_y=code_y
add_arrow_path('a_sem',[(cx-DW/2,y+DH/2),(sem_x+bw/2,y+DH/2),(sem_x+bw/2,sem_y)],S_PURPLE)
n_sem=add_rect('sem',sem_x,sem_y,bw,bh,C_PURPLE,S_PURPLE)
add_text('sem_t',sem_x+5,sem_y+4,bw-10,bh-8,'语义分块\nSemanticChunking()',9,S_PURPLE,'center','middle','sem')
add_text('sem_f',sem_x,sem_y+bh+2,bw,12,'chunking_semantic.go',8,S_GRAY,'left')

# Structure (center-right)
str_x=400;str_y=code_y
add_arrow_path('a_str',[(cx+DW/2,y+DH/2),(str_x+bw/2,y+DH/2),(str_x+bw/2,str_y)],S_BLUE)
n_str=add_rect('str',str_x,str_y,bw,bh,C_BLUE,S_BLUE)
add_text('str_t',str_x+5,str_y+4,bw-10,bh-8,'结构感知分块\nMarkdown标题',9,'#ffffff','center','middle','str')
add_text('str_f',str_x,str_y+bh+2,bw,12,'chunking.go',8,S_GRAY,'left')

# Fixed (far right)
fix_x=600;fix_y=code_y
add_arrow_path('a_fix',[(cx+DW/2,y+DH/2),(fix_x+bw/2,y+DH/2),(fix_x+bw/2,fix_y)],S_BLUE)
n_fix=add_rect('fix',fix_x,fix_y,bw,bh,C_BLUE,S_BLUE)
add_text('fix_t',fix_x+5,fix_y+4,bw-10,bh-8,'固定窗口分块\nChunkDocument()',9,'#ffffff','center','middle','fix')
add_text('fix_f',fix_x,fix_y+bh+2,bw,12,'chunking.go (兜底)',8,S_GRAY,'left')

# Note for chunking
add_rect('ck_n',nx+NW+20,y-6,NW,100,C_GRAY,S_GRAY,1)
add_text('ck_nt',nx+NW+28,y,NW-16,88,'说明: 智能分块策略(按优先级)\n① 代码文件→按函数/类/结构体切分\n   DetectCodeLanguage()自动识别\n② 语义分块→基于Embedding相似度\n   动态决定断点(需API调用)\n③ 结构感知→Markdown标题层次\n   ParseDocument()解析结构\n④ 固定窗口→ChunkSize+Overlap\n   所有格式的最终兜底方案',9,'#374151','left','top','ck_n')

# Merge arrows
merge_y=code_y+bh+GAP+20
add_arrow_path('am1',[bot(n_code),(code_x+bw/2,merge_y-10),(cx,merge_y-10),(cx,merge_y)],S_GREEN)
add_arrow_path('am2',[bot(n_sem),(sem_x+bw/2,merge_y-10),(cx-5,merge_y-10),(cx-5,merge_y)],S_GREEN)
add_arrow_path('am3',[bot(n_str),(str_x+bw/2,merge_y-10),(cx+5,merge_y-10),(cx+5,merge_y)],S_GREEN)
add_arrow_path('am4',[bot(n_fix),(fix_x+bw/2,merge_y-10),(cx+10,merge_y-10),(cx+10,merge_y)],S_GREEN)
y=merge_y

# 3. Prepare texts (Parent-Child)
n3=add_rect('prep',cx-W/2,y,W,H,C_GREEN,S_GREEN)
add_text('prep_t',cx-W/2+5,y+4,W-10,H-8,'Prepare Embedding Texts\nParent-Child: 用子块文本做Embed',10,S_GREEN,'center','middle','prep')
add_rect('prep_n',nx,y-6,NW,56,C_GRAY,S_GRAY,1)
add_text('prep_nt',nx+8,y,NW-16,44,'说明: Parent-Child模式\n• Embedding用子块(粒度细,匹配准)\n• 存储用父块(上下文完整)\n• chunk.EmbeddingContent ≠ Content',9,'#374151','left','top','prep_n')
add_arrow('a04',*bot(n3),cx,y+H+GAP,S_GREEN)
y+=H+GAP

# 4. Batch Loop
add_rect('batch_bg',cx-W/2-20,y-10,W+40,3*(H+GAP)+20,'#f8fafc','#94a3b8',1)
add_text('batch_l',cx-W/2-15,y-6,200,14,'for batch in texts[::batchSize=10]',9,S_GRAY,'left')

# 4a. Cache dedup
n4=add_rect('cache',cx-W/2,y+10,W,H,C_GREEN,S_GREEN)
add_text('cache_t',cx-W/2+5,y+14,W-10,H-8,'Cache Dedup: GetBatch()\n已有向量→跳过API调用',10,S_GREEN,'center','middle','cache')
add_rect('cache_n',nx,y+4,NW,68,C_GRAY,S_GRAY,1)
add_text('cache_nt',nx+8,y+10,NW-16,56,'说明: LRU缓存去重\n• 相同文本命中缓存→零API调用\n• 仅未命中的调 embedWithoutCache\n  (绕过缓存层避免重复查询)\n• 新向量回填缓存供后续使用\n• 重复上传场景大幅省成本',9,'#374151','left','top','cache_n')
add_arrow('a05',*bot(n4),cx,y+10+H+GAP,S_PURPLE)

# 4b. Embed missed
n5=add_rect('emb',cx-W/2,y+10+H+GAP,W,H,C_PURPLE,S_PURPLE)
add_text('emb_t',cx-W/2+5,y+14+H+GAP,W-10,H-8,'embedWithoutCache(missed)\nManager/Direct 三级降级',10,S_PURPLE,'center','middle','emb')
add_text('emb_f',cx-W/2,y+10+2*H+GAP+2,W,12,'embedding_manager.go',9,S_GRAY,'left')
add_arrow('a06',*bot(n5),cx,y+10+2*(H+GAP),S_GREEN)

# 4c. Put cache
n6=add_rect('put',cx-W/2,y+10+2*(H+GAP),W,H,C_GREEN,S_GREEN)
add_text('put_t',cx-W/2+5,y+14+2*(H+GAP),W-10,H-8,'cache.Put() 回填缓存\nallVectors = append(...)',10,S_GREEN,'center','middle','put')
add_arrow('a07',*bot(n6),cx,y+10+3*(H+GAP),S_GREEN)
y=y+10+3*(H+GAP)

# 5. EnsureIndex
n7=add_rect('idx',cx-W/2,y,W,H,C_BLUE,S_BLUE)
add_text('idx_t',cx-W/2+5,y+4,W-10,H-8,'EnsureIndex(vectorDim)\n惰性创建Redis向量索引',10,'#ffffff','center','middle','idx')
add_rect('idx_n',nx,y-6,NW,68,C_GRAY,S_GRAY,1)
add_text('idx_nt',nx+8,y,NW-16,56,'说明: 惰性索引创建\n• 仅首次写入时FT.CREATE\n• 维度从Embedding结果动态推断\n• 支持FLAT/HNSW两种算法\n• 避免为无数据用户浪费资源\n• 索引名包含userID+collection',9,'#374151','left','top','idx_n')
add_arrow('a08',*bot(n7),cx,y+H+GAP,S_GREEN)
y+=H+GAP

# 6. Build entries
n8=add_rect('ent',cx-W/2,y,W,H,C_YELLOW,S_YELLOW)
add_text('ent_t',cx-W/2+5,y+4,W-10,H-8,'Build VectorEntry[]\nfloat64→float32→[]byte 小端序',10,S_YELLOW,'center','middle','ent')
add_rect('ent_n',nx,y-6,NW,56,C_GRAY,S_GRAY,1)
add_text('ent_nt',nx+8,y,NW-16,44,'说明: 数据序列化\n• Key = prefix + chunkID (用户隔离)\n• Fields: content/file_id/chunk_id/vector\n• Parent-Child存parent_chunk_id\n• RediSearch要求float32小端二进制',9,'#374151','left','top','ent_n')
add_arrow('a09',*bot(n8),cx,y+H+GAP,S_GREEN)
y+=H+GAP

# 7. Upsert
n9=add_rect('ups',cx-W/2,y,W,H,C_BLUE,S_BLUE,3)
add_text('ups_t',cx-W/2+5,y+4,W-10,H-8,'UpsertVectors(entries)\nRedis Pipeline 批量写入',10,'#ffffff','center','middle','ups')
add_text('ups_f',cx-W/2,y+H+2,W,12,'store_redis.go / store_milvus.go / store_qdrant.go',8,S_GRAY,'left')
add_rect('ups_n',nx,y-6,NW,56,C_GRAY,S_GRAY,1)
add_text('ups_nt',nx+8,y,NW-16,44,'说明: Pipeline批量写入\n• HSET逐条写入Pipeline缓冲\n• 最后Exec()一次性提交\n• 支持Redis/Milvus/Qdrant多后端\n• 返回成功写入数量',9,'#374151','left','top','ups_n')
add_arrow('a10',*bot(n9),cx,y+H+GAP,S_GREEN)
y+=H+GAP

# 8. Return
n_ret=add_rect('ret',cx-W/2,y,W,H,C_GREEN,S_GREEN,3)
add_text('ret_t',cx-W/2+5,y+4,W-10,H-8,'Return IndexResult\n{FileID, TotalChunks, Indexed, Cached}',10,S_GREEN,'center','middle','ret')
add_arrow('a11',*bot(n_ret),cx,y+H+30,'#c2410c')
n_end=add_ellipse('end',cx-45,y+H+30,90,36,'#fed7aa','#c2410c')
add_text('end_t',cx-30,y+H+36,60,24,'Done',12,'#c2410c','center','middle','end')

doc={'type':'excalidraw','version':2,'source':'https://excalidraw.com','elements':elements,'appState':{'viewBackgroundColor':'#ffffff','gridSize':20},'files':{}}
with open('module-index.excalidraw','w',encoding='utf-8') as f:
    json.dump(doc,f,ensure_ascii=False,indent=2)
print(f'OK! {len(elements)} elements -> module-index.excalidraw')
