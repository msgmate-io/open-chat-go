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
});
