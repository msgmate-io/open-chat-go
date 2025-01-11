import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import vike from "vike/plugin";
import path from "path";

export default defineConfig({
  plugins: [vike({
	  prerender: true
  }), react({}), tailwindcss()],
  build: {
    target: "es2022",
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, '.')
    },
  },
  server: {
    allowedHosts: ["frontend", "localhost", "127.0.0.1", "0.0.0.0"],
    host: "0.0.0.0",
  },
});
