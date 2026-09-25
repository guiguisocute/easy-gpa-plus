import { readdirSync, readFileSync, statSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'
const root=join(dirname(fileURLToPath(import.meta.url)), '..')
const full=process.argv.includes('--full')
const output=full?'frontend/dist':'mock/dist'
const files=[]
const excluded=new Set(['node_modules','dist','.git','.vite','test-results','playwright-report','bin','tmp'])
function collect(dir){for(const entry of readdirSync(join(root,dir),{withFileTypes:true})){
 if(excluded.has(entry.name)||entry.name.startsWith('.env')||entry.name.endsWith('.tsbuildinfo')||entry.name.endsWith('.exe'))continue
 const path=join(dir,entry.name)
 if(entry.isDirectory())collect(path);else if(entry.isFile())files.push(path)
}}
for(const dir of (full?['frontend','mock','backend','asset','deploy','scripts','examples','e2e','.github']:['frontend','mock','examples']))collect(dir)
if(full)files.push('compose.yaml','compose.images.yaml','compose.dev.yaml','compose.e2e.yaml','.dockerignore','.env.example','.gitignore','.gitattributes','.editorconfig','Makefile','AGENTS.md','CONTRIBUTING.md')
files.push('LICENSE', 'NOTICE','README.md','CONTEXT.md','scripts/package-demo-source.mjs','scripts/deploy-mock.mjs','scripts/mcp-http.mjs','scripts/mcp-bridge.mjs','scripts/mcp-upload.mjs')
const blocks=[]
function text(h,start,len,value){h.write(value,start,len,'utf8')}
function octal(h,start,len,value){text(h,start,len,value.toString(8).padStart(len-1,'0')+'\0')}
for(const path of [...new Set(files)].sort()){
 const data=readFileSync(join(root,path)),h=Buffer.alloc(512),name=relative(root,join(root,path)).replaceAll('\\','/')
 let shortName=name; if(Buffer.byteLength(name)>100){const split=name.lastIndexOf('/');const prefix=name.slice(0,split);shortName=name.slice(split+1);if(Buffer.byteLength(prefix)>155 || Buffer.byteLength(shortName)>100)throw new Error('Source path too long');text(h,345,155,prefix)}
 text(h,0,100,shortName);octal(h,100,8,0o644);octal(h,108,8,0);octal(h,116,8,0);octal(h,124,12,data.length);octal(h,136,12,Math.floor(statSync(join(root,path)).mtimeMs/1000));h.fill(32,148,156);text(h,156,1,'0');text(h,257,6,'ustar\0');text(h,263,2,'00')
 const sum=h.reduce((a,b)=>a+b,0);text(h,148,8,sum.toString(8).padStart(6,'0')+'\0 ')
 blocks.push(h,data,Buffer.alloc((512-data.length%512)%512))
}
blocks.push(Buffer.alloc(1024));mkdirSync(join(root,output),{recursive:true});writeFileSync(join(root,output,'source.tar.gz'),gzipSync(Buffer.concat(blocks)))
console.log(`Packaged ${files.length} source files (no runtime settings or dependencies).`)
