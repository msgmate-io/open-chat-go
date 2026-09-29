import vikeReact from "vike-react/config";
import type { Config } from "vike/types";
import Layout from "../layouts/LayoutDefault.js";

const isMobileClientRenderBuild = process.env.OPENCHAT_SSR_MODE === "csr";

// Default config (can be overridden by pages)
// https://vike.dev/config

export default {
  // https://vike.dev/Layout
  Layout,

  // https://vike.dev/head-tags
  title: "Open-Chat",
  description: "Open-Chat Go is an federated chat app, build for the msgmate ai",
  extends: vikeReact,
  passToClient: [
    'pageContext'
  ],
  htmlAttributes: {
    lang: "en",
    // "data-theme": "light",
  },
  ssr: !isMobileClientRenderBuild,
  prerender: true,
} satisfies Config;
