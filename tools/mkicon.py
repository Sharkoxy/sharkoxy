#!/usr/bin/env python3
"""Draws the Sharkoxy mascot (shark giving a thumbs up) as 32x32 pixel art.
Writes assets/app.ico, ui/logo.svgfrag and a preview PNG. Needs Pillow."""
import sys, io, struct
from PIL import Image
N=32
BL=(41,173,255,255); NV=(29,43,83,255); WH=(255,241,232,255); RD=(255,0,77,255); LB=(131,214,255,255)
g=[[None]*N for _ in range(N)]
def ell(cx,cy,rx,ry,c,clip=None):
    for y in range(N):
        for x in range(N):
            if ((x+.5-cx)/rx)**2+((y+.5-cy)/ry)**2<=1 and (clip is None or clip(x,y)): g[y][x]=c
def rect(x0,y0,x1,y1,c):
    for y in range(y0,y1+1):
        for x in range(x0,x1+1): g[y][x]=c
def tri(p,q,r,c):
    def s(a,b,c_): return (a[0]-c_[0])*(b[1]-c_[1])-(b[0]-c_[0])*(a[1]-c_[1])
    for y in range(N):
        for x in range(N):
            t=(x+.5,y+.5); d1,d2,d3=s(t,p,q),s(t,q,r),s(t,r,p)
            if not((d1<0 or d2<0 or d3<0) and (d1>0 or d2>0 or d3>0)): g[y][x]=c
# dorsal fin + body (wide, shark-like)
tri((10,5),(5,13),(15,13),BL)
ell(12,20,12,8.5,BL)
ell(12,25,8,4.5,WH,clip=lambda x,y:((x+.5-12)/12)**2+((y+.5-20)/8.5)**2<=1 and y>=23)
for y in range(8,12): g[y][9]=LB
# eyes
for ex in (6,16):
    rect(ex,16,ex+1,18,NV); g[16][ex]=WH
g[20][4]=RD; g[20][19]=RD
# mouth + teeth
rect(7,21,16,21,NV)
for x in (8,10,12,14): g[22][x]=WH
for x in (9,11,13,15): g[22][x]=NV
# thumbs-up flipper
rect(22,17,30,27,BL)      # fist
rect(25,8,28,17,BL)       # thumb
for y in (20,23,26): rect(23,y,29,y,LB) if False else None
for y in (21,24): rect(24,y,29,y,LB)
# outline
src=[r[:] for r in g]
for y in range(N):
    for x in range(N):
        if src[y][x] is None:
            for dx,dy in((1,0),(-1,0),(0,1),(0,-1)):
                xx,yy=x+dx,y+dy
                if 0<=xx<N and 0<=yy<N and src[yy][xx] not in(None,NV): g[y][x]=NV;break
# tail nub
def img(bg=None):
    im=Image.new("RGBA",(N,N),bg or (0,0,0,0))
    for y in range(N):
        for x in range(N):
            if g[y][x]: im.putpixel((x,y),g[y][x])
    return im
if __name__=="__main__":
    out=sys.argv[1]
    base=img((126,37,83,255))   # plum tile, same as the in-app tile
    for p in range(N):
        pass
    sizes=[16,24,32,48,64,128,256]
    pngs=[]
    for s in sizes:
        b=io.BytesIO(); base.resize((s,s),Image.NEAREST).save(b,"PNG"); pngs.append(b.getvalue())
    hdr=struct.pack("<HHH",0,1,len(sizes)); off=6+16*len(sizes); ent=b""
    for s,p in zip(sizes,pngs):
        ent+=struct.pack("<BBBBHHII",s%256,s%256,0,0,1,32,len(p),off); off+=len(p)
    open(out+"/app.ico","wb").write(hdr+ent+b"".join(pngs))
    base.resize((256,256),Image.NEAREST).save(out+"/logo-preview.png")
    # svg: tile + run-merged rects
    s=['<svg class="logo" viewBox="0 0 32 32" shape-rendering="crispEdges"><rect width="32" height="32" fill="#7e2553"/>']
    for y in range(N):
        x=0
        while x<N:
            c=g[y][x]
            if not c: x+=1; continue
            x2=x
            while x2+1<N and g[y][x2+1]==c: x2+=1
            s.append('<rect x="%d" y="%d" width="%d" height="1" fill="#%02x%02x%02x"/>'%(x,y,x2-x+1,*c[:3])); x=x2+1
    s.append('</svg>')
    open(out+"/logo.svgfrag","w").write("".join(s))
