package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/your-org/fd/internal/api/live"
	"github.com/your-org/fd/internal/storage"
)

const mjpegBoundary = "mjpegboundary"

// LiveHandler serves MJPEG live stream and the web UI.
type LiveHandler struct {
	cache  *live.FrameCache
	minio  *storage.MinIOStore
	apiKey string
}

func NewLiveHandler(cache *live.FrameCache, minio *storage.MinIOStore, apiKey string) *LiveHandler {
	return &LiveHandler{cache: cache, minio: minio, apiKey: apiKey}
}

// MJPEG streams the latest frames as multipart/x-mixed-replace.
// Frames are fetched from MinIO as they arrive via FrameCache.
func (h *LiveHandler) MJPEG(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	c.Writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+mjpegBoundary)
	c.Writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	ch := h.cache.Subscribe(streamID)
	defer h.cache.Unsubscribe(streamID, ch)

	ctx := c.Request.Context()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastKey string

	for {
		select {
		case <-ctx.Done():
			return
		case key, ok := <-ch:
			if !ok {
				return
			}
			lastKey = key
		case <-ticker.C:
			if lastKey == "" {
				continue
			}
		}

		if lastKey == "" {
			continue
		}

		if err := h.sendMJPEGFrame(ctx, c, lastKey); err != nil {
			return
		}
	}
}

func (h *LiveHandler) sendMJPEGFrame(ctx context.Context, c *gin.Context, frameKey string) error {
	data, err := h.minio.GetObject(ctx, frameKey)
	if err != nil {
		slog.Warn("live: get frame from minio", "key", frameKey, "error", err)
		return nil
	}

	header := fmt.Sprintf("--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
		mjpegBoundary, len(data))
	if _, err := fmt.Fprint(c.Writer, header); err != nil {
		return err
	}
	if _, err := c.Writer.Write(data); err != nil {
		return err
	}
	if _, err := fmt.Fprint(c.Writer, "\r\n"); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

// UI serves the single-page live viewer HTML.
func (h *LiveHandler) UI(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, uiHTML)
}

const uiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>FD Live</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#111;color:#eee;font-family:monospace;height:100vh;display:flex;flex-direction:column}
.hdr{padding:9px 14px;background:#1a1a1a;border-bottom:1px solid #333;display:flex;align-items:center;gap:9px;flex-shrink:0}
h1{font-size:15px;color:#4af;white-space:nowrap}
select,input,button{background:#222;color:#eee;border:1px solid #444;padding:5px 10px;font-family:monospace;font-size:12px;cursor:pointer}
select{flex:1;max-width:380px}
input{width:130px}
button{border-radius:2px}
button:hover:not(:disabled){background:#2d2d2d}
button:disabled{opacity:.4;cursor:default}
.ws-st{font-size:11px;color:#666;white-space:nowrap;margin-left:auto}
.dot{display:inline-block;width:7px;height:7px;border-radius:50%;background:#f44;animation:blink 1s infinite;margin-right:3px;vertical-align:middle}
.dot.on{background:#4f4;animation:none}
@keyframes blink{0%,100%{opacity:1}50%{opacity:.15}}
.main{display:flex;flex:1;overflow:hidden}
.vp{flex:1;position:relative;background:#000;display:flex;align-items:center;justify-content:center;overflow:hidden}
#stream{max-width:100%;max-height:100%;display:none}
#overlay{position:absolute;pointer-events:none;display:none}
.nosig{color:#2a2a2a;font-size:13px;user-select:none}
.sb{width:300px;background:#1a1a1a;border-left:1px solid #333;display:flex;flex-direction:column;flex-shrink:0}
/* tabs */
.tabs{display:flex;border-bottom:1px solid #333;flex-shrink:0}
.tab-btn{flex:1;padding:7px 0;background:#1a1a1a;border:none;border-radius:0;color:#555;font-size:11px;letter-spacing:.5px;cursor:pointer}
.tab-btn.active{color:#4af;border-bottom:2px solid #4af;margin-bottom:-1px}
.tab-btn:hover{color:#ccc}
/* panels */
.panel{display:none;flex-direction:column;flex:1;overflow:hidden}
.panel.active{display:flex}
.sb-hdr{padding:7px 10px;border-bottom:1px solid #333;font-size:11px;color:#555;text-transform:uppercase;letter-spacing:.5px;display:flex;justify-content:space-between;flex-shrink:0}
.arch-info{padding:6px 10px;border-bottom:1px solid #222;font-size:11px;color:#555;flex-shrink:0}
.ev-list{flex:1;overflow-y:auto;padding:6px}
.arch-nav{display:flex;gap:6px;padding:7px 10px;border-top:1px solid #222;flex-shrink:0}
.arch-nav button{flex:1}
#stbar{padding:4px 10px;font-size:10px;color:#444;border-top:1px solid #222;flex-shrink:0}
/* card */
.card{background:#1e1e1e;border:1px solid #2e2e2e;border-radius:3px;margin-bottom:5px;cursor:pointer;display:flex;overflow:hidden;transition:background .1s}
.card:hover{background:#252525;border-color:#444}
.card.recog{border-left:3px solid #2a7}
.card.unk{border-left:3px solid #a60}
.snap{width:58px;min-height:58px;background:#161616;display:flex;align-items:center;justify-content:center;flex-shrink:0;overflow:hidden}
.snap img{width:58px;height:58px;object-fit:cover;display:block}
.snap-ph{font-size:22px;color:#333}
.cbody{padding:6px 8px;flex:1;min-width:0;display:flex;flex-direction:column;justify-content:center}
.ctype{font-size:10px;color:#555;margin-bottom:2px}
.cname{font-size:13px;color:#3d6;font-weight:bold;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-bottom:2px}
.cattr{font-size:11px;color:#888}
.cscore{color:#fc3}
.chint{font-size:10px;color:#444;margin-top:3px}
/* modal */
.modal{display:none;position:fixed;inset:0;z-index:200;align-items:center;justify-content:center}
.modal.open{display:flex}
.moverlay{position:absolute;inset:0;background:rgba(0,0,0,.85)}
.mbox{position:relative;background:#181818;border:1px solid #3a3a3a;border-radius:4px;display:flex;flex-direction:column;overflow:hidden;max-width:92vw;max-height:92vh}
.mhdr{padding:8px 12px;background:#222;display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid #333;flex-shrink:0}
.mtitle{font-size:14px;font-weight:bold;color:#eee}
.mclose{background:none;border:1px solid #555;color:#aaa;width:26px;height:26px;cursor:pointer;font-size:14px;border-radius:2px;display:flex;align-items:center;justify-content:center}
.mclose:hover{background:#333;color:#fff}
.mbody{display:flex;flex:1;overflow:hidden;min-height:0}
.fcol{flex:1;overflow:auto;background:#000;display:flex;align-items:flex-start;justify-content:flex-start;min-width:0}
.fwrap{position:relative;display:inline-block;line-height:0}
.fwrap img{display:block;max-width:calc(92vw - 230px);max-height:calc(92vh - 44px)}
.fwrap canvas{position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none}
.fmsg{color:#444;padding:30px 20px;font-size:13px;display:none}
.icol{width:220px;padding:12px;overflow-y:auto;flex-shrink:0;border-left:1px solid #2a2a2a;font-size:12px}
.irow{margin-bottom:10px}
.ilbl{color:#555;font-size:10px;text-transform:uppercase;letter-spacing:.4px;margin-bottom:3px}
.ival{color:#ccc}
.ival.grn{color:#4e6}
.ival.yel{color:#fc3}
.ival.dim{color:#555;font-size:10px}
.isnap{margin-top:4px}
.isnap img{width:100%;border:1px solid #333;border-radius:2px}
</style>
</head>
<body>
<div class="hdr">
  <h1>&#9679; FD Live</h1>
  <select id="sSel" onchange="onStreamChange()"><option value="">&#8212; select stream &#8212;</option></select>
  <input id="apiK" type="text" placeholder="api key">
  <button onclick="startV()">&#9654; Watch</button>
  <button onclick="stopV()">&#9632; Stop</button>
  <button id="btnLoad" style="display:none" onclick="archLoad(0)">&#8635; Reload</button>
  <span class="ws-st"><span class="dot" id="dot"></span><span id="wsLbl">disconnected</span></span>
</div>
<div class="main">
  <div class="vp">
    <img id="stream" alt="">
    <canvas id="overlay"></canvas>
    <div class="nosig" id="nosig">Select a stream and press Watch</div>
  </div>
  <div class="sb">
    <div class="tabs">
      <button class="tab-btn active" id="tbLive" onclick="showTab('live')">LIVE</button>
      <button class="tab-btn" id="tbArch" onclick="showTab('archive')">ARCHIVE</button>
    </div>
    <div id="panLive" class="panel active">
      <div class="sb-hdr"><span>Detections</span><span id="evCnt">0</span></div>
      <div class="ev-list" id="evList"></div>
    </div>
    <div id="panArch" class="panel">
      <div class="arch-info" id="archInfo">No data — press Reload</div>
      <div class="ev-list" id="archList"></div>
      <div class="arch-nav">
        <button id="archPrev" disabled onclick="archPrevFn()">&#8592; Prev</button>
        <button id="archNext" disabled onclick="archNextFn()">Next &#8594;</button>
      </div>
    </div>
    <div id="stbar">Ready</div>
  </div>
</div>
<div class="modal" id="modal">
  <div class="moverlay" onclick="closeM()"></div>
  <div class="mbox">
    <div class="mhdr">
      <span class="mtitle" id="mTitle">Detection</span>
      <button class="mclose" onclick="closeM()">&#10005;</button>
    </div>
    <div class="mbody">
      <div class="fcol">
        <div class="fwrap" id="fwrap">
          <img id="mImg" alt="">
          <canvas id="mCanvas"></canvas>
        </div>
        <div class="fmsg" id="fmsg">Frame not available</div>
      </div>
      <div class="icol" id="iCol"></div>
    </div>
  </div>
</div>
<script>
(function(){
  var ws=null, cur=null, dets=new Map(), raf=null;
  var liveKeys=new Map(); // trackId -> evH key
  var evH=new Map();      // key -> event object
  var liveNewCnt=0;
  var archOffset=0, archTotal=0, ARCH_LIMIT=20;
  var KEY=localStorage.getItem('fd_key')||'';

  document.getElementById('apiK').value=KEY;
  document.getElementById('apiK').oninput=function(){
    KEY=this.value;localStorage.setItem('fd_key',KEY);
    clearTimeout(document._lsTimer);
    document._lsTimer=setTimeout(loadStreams,400);
  };

  function q(u){return u+(u.indexOf('?')>=0?'&':'?')+'api_key='+encodeURIComponent(KEY);}

  /* ---- tabs ---- */
  window.showTab=function(name){
    document.getElementById('panLive').className='panel'+(name==='live'?' active':'');
    document.getElementById('panArch').className='panel'+(name==='archive'?' active':'');
    document.getElementById('tbLive').className='tab-btn'+(name==='live'?' active':'');
    document.getElementById('tbArch').className='tab-btn'+(name==='archive'?' active':'');
    document.getElementById('btnLoad').style.display=name==='archive'?'':'none';
    if(name==='archive'&&archTotal===0)archLoad(0);
  };

  /* ---- streams ---- */
  function loadStreams(){
    fetch('/v1/streams',{headers:{'X-API-Key':KEY}})
      .then(function(r){
        if(!r.ok)throw new Error('HTTP '+r.status);
        return r.json();
      })
      .then(function(d){
        var sel=document.getElementById('sSel');
        var prev=sel.value;
        // clear all except placeholder
        while(sel.options.length>1)sel.remove(1);
        (d.streams||[]).forEach(function(s){
          var o=document.createElement('option');
          o.value=s.id;
          var lbl=s.url.replace(/^rtsp:\/\//,'');
          if(lbl.length>45)lbl=lbl.slice(0,42)+'...';
          o.textContent=lbl+' ['+s.status+']';
          sel.appendChild(o);
        });
        // restore previous selection if still present
        if(prev)sel.value=prev;
        st((d.streams||[]).length+' stream(s) loaded');
      }).catch(function(e){st('Streams error: '+e.message);});
  }

  window.onStreamChange=function(){
    archOffset=0;archTotal=0;
    document.getElementById('archInfo').textContent='No data — press Reload';
    document.getElementById('archList').innerHTML='';
    document.getElementById('archPrev').disabled=true;
    document.getElementById('archNext').disabled=true;
  };

  /* ---- viewer ---- */
  window.startV=function(){
    var id=document.getElementById('sSel').value;
    if(!id){st('Select a stream first');return;}
    stopV();cur=id;
    var img=document.getElementById('stream');
    img.src='/v1/streams/'+id+'/live?api_key='+encodeURIComponent(KEY);
    img.style.display='block';
    document.getElementById('overlay').style.display='block';
    document.getElementById('nosig').style.display='none';
    connectWS(id);
    raf=requestAnimationFrame(draw);
  };

  window.stopV=function(){
    if(ws){ws.close();ws=null;}
    cur=null;dets.clear();
    var img=document.getElementById('stream');
    img.src='';img.style.display='none';
    document.getElementById('overlay').style.display='none';
    document.getElementById('nosig').style.display='block';
    if(raf){cancelAnimationFrame(raf);raf=null;}
    setDot(false);st('Stopped');
  };

  /* ---- websocket ---- */
  function connectWS(sid){
    if(ws)ws.close();
    var p=location.protocol==='https:'?'wss:':'ws:';
    ws=new WebSocket(p+'//'+location.host+'/v1/ws?stream_id='+sid+'&api_key='+encodeURIComponent(KEY));
    ws.onopen=function(){setDot(true);st('Live');};
    ws.onmessage=function(e){try{onWS(JSON.parse(e.data));}catch(_){}};
    ws.onclose=function(){setDot(false);if(cur)setTimeout(function(){if(cur)connectWS(cur);},3000);};
  }

  function onWS(msg){
    if(msg.stream_id!==cur)return;
    if(msg.type!=='face_detected'&&msg.type!=='face_recognized')return;
    var d=msg.data, rec=msg.type==='face_recognized';
    var label=rec
      ?(d.matched_name||(d.matched_person_id?'ID:'+d.matched_person_id.slice(0,8):'Unknown'))
      :((d.gender||'?')+', '+(d.age||'?')+'y');
    var evKey='live:'+d.track_id;
    var isNew=!liveKeys.has(d.track_id);
    var ev={
      key:evKey,id:d.id,trackId:d.track_id,
      frameUrl:d.frame_url,snapUrl:d.snapshot_url,
      bbox:msg.bbox,fw:msg.frame_width,fh:msg.frame_height,
      label:label,color:rec?'#44ee77':'#ff8833',
      gender:d.gender,age:d.age,ageRange:d.age_range,
      score:d.match_score,conf:d.confidence,rec:rec,
      ts:d.timestamp,matchId:d.matched_person_id,
      tsStr:d.timestamp?new Date(d.timestamp).toLocaleTimeString():''
    };
    evH.set(evKey,ev);
    liveKeys.set(d.track_id,evKey);
    dets.set(d.track_id,{bbox:msg.bbox,fw:msg.frame_width,fh:msg.frame_height,
      label:label,color:ev.color,exp:Date.now()+4000});
    upsertLiveCard(ev,isNew);
    st('Last: '+new Date().toLocaleTimeString());
  }

  /* ---- live cards (deduplicated by track_id) ---- */
  function upsertLiveCard(ev,isNew){
    var list=document.getElementById('evList');
    var cid='LC'+ev.trackId.replace(/\W/g,'_');
    var old=document.getElementById(cid);if(old)old.remove();
    list.insertBefore(makeCard(ev,cid),list.firstChild);
    if(isNew){liveNewCnt++;document.getElementById('evCnt').textContent=liveNewCnt;}
    while(list.children.length>80)list.removeChild(list.lastChild);
  }

  /* ---- archive ---- */
  window.archLoad=function(offset){
    var id=document.getElementById('sSel').value;
    if(!id){st('Select a stream first');return;}
    archOffset=offset;
    fetch(q('/v1/streams/'+id+'/events?limit='+ARCH_LIMIT+'&offset='+offset),{headers:{'X-API-Key':KEY}})
      .then(function(r){return r.json();})
      .then(function(d){
        archTotal=d.total||0;
        var evs=d.events||[];
        var list=document.getElementById('archList');
        list.innerHTML='';
        if(evs.length===0){
          document.getElementById('archInfo').textContent='No events found';
        } else {
          document.getElementById('archInfo').textContent=
            (offset+1)+' – '+(offset+evs.length)+' / '+archTotal;
        }
        evs.forEach(function(e){
          var rec=!!(e.matched_person_id);
          var label=rec
            ?(e.matched_name||(e.matched_person_id?'ID:'+e.matched_person_id.slice(0,8):'Unknown'))
            :((e.gender||'?')+', '+(e.age||'?')+'y');
          var evKey='arch:'+e.id;
          var ev={
            key:evKey,id:e.id,trackId:e.track_id,
            frameUrl:e.frame_url,snapUrl:e.snapshot_url,
            bbox:e.bbox,fw:e.frame_width,fh:e.frame_height,
            label:label,color:rec?'#44ee77':'#ff8833',
            gender:e.gender,age:e.age,ageRange:e.age_range,
            score:e.match_score,conf:e.confidence,rec:rec,
            ts:e.timestamp,matchId:e.matched_person_id,
            tsStr:e.timestamp?new Date(e.timestamp).toLocaleString():''
          };
          evH.set(evKey,ev);
          list.appendChild(makeCard(ev,'AC'+e.id));
        });
        document.getElementById('archPrev').disabled=(offset<=0);
        document.getElementById('archNext').disabled=(offset+evs.length>=archTotal);
      }).catch(function(e){st('Archive error: '+e.message);});
  };

  window.archPrevFn=function(){if(archOffset>=ARCH_LIMIT)archLoad(archOffset-ARCH_LIMIT);};
  window.archNextFn=function(){archLoad(archOffset+ARCH_LIMIT);};

  /* ---- shared card factory ---- */
  function makeCard(ev,domId){
    var c=document.createElement('div');
    c.id=domId;
    c.className='card '+(ev.rec?'recog':'unk');
    var sc=ev.score?(+(ev.score*100).toFixed(1)+'%'):'';
    var snapSrc=ev.snapUrl?q(ev.snapUrl):'';
    c.innerHTML=
      '<div class="snap">'+
        (snapSrc
          ?'<img src="'+snapSrc+'" onerror="this.parentNode.innerHTML=\'<span class=snap-ph>&#128100;</span>\'" alt="">'
          :'<span class="snap-ph">&#128100;</span>')+
      '</div>'+
      '<div class="cbody">'+
        '<div class="ctype">'+(ev.rec?'&#10003; RECOGNIZED':'&#8226; UNKNOWN')+
          (ev.tsStr?' &middot; '+esc(ev.tsStr):'')+
        '</div>'+
        (ev.rec?'<div class="cname">'+esc(ev.label)+'</div>':'')+
        '<div class="cattr">'+esc(ev.gender||'')+' '+(ev.age?ev.age+'y':'')+
          (ev.ageRange?' ('+esc(ev.ageRange)+')':'')+
          (sc?' <span class="cscore">'+sc+'</span>':'')+
        '</div>'+
        (ev.frameUrl?'<div class="chint">&#128065; click to view frame</div>':'')+
      '</div>';
    var k=ev.key;
    c.onclick=function(){openM(k);};
    return c;
  }

  /* ---- modal ---- */
  window.openM=function(evKey){
    var ev=evH.get(evKey);if(!ev)return;
    document.getElementById('mTitle').textContent=ev.label;
    document.getElementById('modal').classList.add('open');
    var ic=document.getElementById('iCol');
    ic.innerHTML=
      row('Type',ev.rec?'<span class="ival grn">Recognized</span>':'<span class="ival">Unknown</span>')+
      (ev.rec?row('Name','<span class="ival grn">'+esc(ev.label)+'</span>'):'')+
      (ev.score?row('Score','<span class="ival yel">'+(ev.score*100).toFixed(1)+'%</span>'):'')+
      row('Gender','<span class="ival">'+esc(ev.gender||'—')+'</span>')+
      row('Age','<span class="ival">'+(ev.age?ev.age+'y':'—')+(ev.ageRange?' ('+esc(ev.ageRange)+')':'')+'</span>')+
      row('Conf','<span class="ival">'+(ev.conf?(ev.conf*100).toFixed(0)+'%':'—')+'</span>')+
      row('Time','<span class="ival">'+(ev.ts?new Date(ev.ts).toLocaleString():'—')+'</span>')+
      row('Track','<span class="ival dim">'+esc(ev.trackId||'—')+'</span>')+
      (ev.snapUrl?'<div class="irow"><div class="ilbl">Face</div><div class="isnap"><img src="'+q(ev.snapUrl)+'" onerror="this.style.display=\'none\'" alt=""></div></div>':'');
    var mi=document.getElementById('mImg');
    var mc=document.getElementById('mCanvas');
    var fw=document.getElementById('fwrap');
    var fm=document.getElementById('fmsg');
    fm.style.display='none';fw.style.display='inline-block';
    mc.width=0;mc.height=0;mi.src='';
    if(ev.frameUrl){
      mi.onload=function(){
        var nw=mi.naturalWidth,nh=mi.naturalHeight;
        mc.width=nw;mc.height=nh;
        var ctx=mc.getContext('2d');
        ctx.clearRect(0,0,nw,nh);
        if(ev.bbox&&ev.bbox.length===4&&ev.fw&&ev.fh){
          var sx=nw/ev.fw,sy=nh/ev.fh;
          var x=ev.bbox[0]*sx,y=ev.bbox[1]*sy;
          var w=(ev.bbox[2]-ev.bbox[0])*sx,h=(ev.bbox[3]-ev.bbox[1])*sy;
          var lw=Math.max(3,nw/400);
          ctx.strokeStyle=ev.color;ctx.lineWidth=lw;
          ctx.strokeRect(x,y,w,h);
          var fs=Math.max(16,nw/80);
          ctx.font='bold '+fs+'px monospace';
          var tw=ctx.measureText(ev.label).width,pad=6;
          ctx.fillStyle=ev.color;
          ctx.fillRect(x-lw/2,y-fs-pad*2,tw+pad*2,fs+pad*2);
          ctx.fillStyle='#000';
          ctx.fillText(ev.label,x+pad-lw/2,y-pad);
        }
      };
      mi.onerror=function(){fw.style.display='none';fm.style.display='block';};
      mi.src=q(ev.frameUrl);
    } else {
      fw.style.display='none';fm.style.display='block';
    }
  };

  window.closeM=function(){
    document.getElementById('modal').classList.remove('open');
    document.getElementById('mImg').src='';
    document.getElementById('mCanvas').width=0;
  };

  document.addEventListener('keydown',function(e){if(e.key==='Escape')closeM();});

  /* ---- draw loop ---- */
  function draw(){
    if(!cur)return;
    raf=requestAnimationFrame(draw);
    var now=Date.now();
    var img=document.getElementById('stream');
    var cv=document.getElementById('overlay');
    if(!img.offsetWidth)return;
    var ir=img.getBoundingClientRect(),pr=img.parentElement.getBoundingClientRect();
    cv.width=img.offsetWidth;cv.height=img.offsetHeight;
    cv.style.left=(ir.left-pr.left)+'px';cv.style.top=(ir.top-pr.top)+'px';
    cv.style.width=img.offsetWidth+'px';cv.style.height=img.offsetHeight+'px';
    var ctx=cv.getContext('2d');
    ctx.clearRect(0,0,cv.width,cv.height);
    dets.forEach(function(det,tid){
      if(det.exp<now){dets.delete(tid);return;}
      if(!det.bbox||!det.fw||!det.fh)return;
      var sx=cv.width/det.fw,sy=cv.height/det.fh;
      var x=det.bbox[0]*sx,y=det.bbox[1]*sy;
      var w=(det.bbox[2]-det.bbox[0])*sx,h=(det.bbox[3]-det.bbox[1])*sy;
      ctx.strokeStyle=det.color;ctx.lineWidth=2;
      ctx.strokeRect(x,y,w,h);
      ctx.font='bold 12px monospace';
      var tw=ctx.measureText(det.label).width;
      ctx.fillStyle=det.color;ctx.fillRect(x-1,y-18,tw+8,18);
      ctx.fillStyle='#000';ctx.fillText(det.label,x+3,y-5);
    });
  }

  /* ---- helpers ---- */
  function row(l,v){return '<div class="irow"><div class="ilbl">'+l+'</div><div>'+v+'</div></div>';}
  function setDot(on){
    document.getElementById('dot').className='dot'+(on?' on':'');
    document.getElementById('wsLbl').textContent=on?'live':'disconnected';
  }
  function st(t){document.getElementById('stbar').textContent=t;}
  function esc(s){return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

  loadStreams();
})();
</script>
</body>
</html>`
