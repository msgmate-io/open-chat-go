import type { Config } from "tailwindcss";
const daisyuiColorObj = require("daisyui/src/colors/index");

const screens = {
  sm: "640px",
  // => @media (min-width: 640px) { ... }

  md: "768px",
  // => @media (min-width: 768px) { ... }

  lg: "1024px",
  // => @media (min-width: 1024px) { ... }

  xl: "1280px",
  // => @media (min-width: 1280px) { ... }

  "2xl": "1536px",
  // => @media (min-width: 1536px) { ... }
}

module.exports = {
  darkMode: ["class"],
  content: [
    "./pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./layouts/**/*.{js,ts,jsx,tsx,mdx}",
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  prefix: "",
  screens,
  theme: {
  	container: {
  		center: true,
  		padding: '2rem'
  	},
  	extend: {
  		colors: {
  			border: 'daisyuiColorObj["base-content"],',
  			input: 'daisyuiColorObj["base-content"],',
  			ring: 'daisyuiColorObj["base-content"],',
  			background: 'daisyuiColorObj["base-100"],',
  			foreground: 'daisyuiColorObj["base-content"],',
  			primary: {
  				DEFAULT: 'daisyuiColorObj["primary"],',
  				foreground: 'daisyuiColorObj["primary-content"]
  			},
  			secondary: {
  				DEFAULT: 'daisyuiColorObj["secondary"],',
  				foreground: 'daisyuiColorObj["secondary-content"]
  			},
  			destructive: {
  				DEFAULT: 'daisyuiColorObj["error"],',
  				foreground: 'daisyuiColorObj["error-content"]
  			},
  			muted: {
  				DEFAULT: 'daisyuiColorObj["base-300"],',
  				foreground: 'daisyuiColorObj["base-content"]
  			},
  			accent: {
  				DEFAULT: 'daisyuiColorObj["accent"],',
  				foreground: 'daisyuiColorObj["accent-content"]
  			},
  			popover: {
  				DEFAULT: 'daisyuiColorObj["base-100"],',
  				foreground: 'daisyuiColorObj["base-content"]
  			},
  			card: {
  				DEFAULT: 'daisyuiColorObj["base-100"],',
  				foreground: 'daisyuiColorObj["base-content"]
  			},
  			zIndex: {
  				'60': '60'
  			},
  			sidebar: {
  				DEFAULT: 'hsl(var(--sidebar-background))',
  				foreground: 'hsl(var(--sidebar-foreground))',
  				primary: 'hsl(var(--sidebar-primary))',
  				'primary-foreground': 'hsl(var(--sidebar-primary-foreground))',
  				accent: 'hsl(var(--sidebar-accent))',
  				'accent-foreground': 'hsl(var(--sidebar-accent-foreground))',
  				border: 'hsl(var(--sidebar-border))',
  				ring: 'hsl(var(--sidebar-ring))'
  			}
  		},
  		borderRadius: {
  			lg: 'var(--radius)',
  			md: 'calc(var(--radius) - 2px)',
  			sm: 'calc(var(--radius) - 4px)'
  		},
  		keyframes: {
  			'accordion-down': {
  				from: {
  					height: '0'
  				},
  				to: {
  					height: 'var(--radix-accordion-content-height)'
  				}
  			},
  			'accordion-up': {
  				from: {
  					height: 'var(--radix-accordion-content-height)'
  				},
  				to: {
  					height: '0'
  				}
  			}
  		},
  		animation: {
  			'accordion-down': 'accordion-down 0.2s ease-out',
  			'accordion-up': 'accordion-up 0.2s ease-out'
  		}
  	}
  },
  plugins: [require("daisyui"), require('@tailwindcss/typography'), require("tailwindcss-animate")],
  daisyui: {
    themes: ["light", "dark", "cupcake", "retro"],
  },
};
