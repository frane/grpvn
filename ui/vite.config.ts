import { defineConfig } from 'vite';
export default defineConfig({build:{outDir:'dist',emptyOutDir:true},server:{proxy:{'/v1':'http://127.0.0.1:7331','/join':'http://127.0.0.1:7331'}}});
