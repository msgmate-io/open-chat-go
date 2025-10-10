import type { Config } from "vike/types";

export default {
	// Static route only; ?url= is a client-side query parameter.
	prerender: true,
} satisfies Config;
