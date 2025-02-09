import vikeReact from "vike-react/config";
import type { Config } from "vike/types";
import Layout from "../layouts/LayoutDefault.js";

// Default config (can be overridden by pages)
// https://vike.dev/config

export default {
  // https://vike.dev/Layout
  Layout,

  // https://vike.dev/head-tags
  title: "Open-Chat Go",
  description: "Open-Chat Go is an federated chat app, build for the msgmate ai",
  extends: vikeReact,
  passToClient: [
    'pageContext'
  ],
  htmlAttributes: {
    lang: "en",
    // "data-theme": "light",
  },
  ssr: false,
  prerender: true,
} satisfies Config;
