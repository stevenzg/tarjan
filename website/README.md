# tarjan website

The marketing landing page (`/`) and documentation (`/docs`, built with
[Fumadocs](https://fumadocs.dev)) for tarjan. It's a Next.js app that exports to
fully static HTML.

```bash
npm install        # also generates the Fumadocs source (postinstall)
npm run dev        # http://localhost:3000
npm run build      # static export to ./out
```

- Landing page: `app/page.tsx`
- Docs content: `content/docs/*.mdx` (ordering in `content/docs/meta.json`)
- Docs routing/layout: `app/docs/`, `lib/source.ts`, `source.config.ts`

Deployment to GitHub Pages is automated by `.github/workflows/pages.yml`
(set `PAGES_BASE_PATH=/<repo>` for project pages).
