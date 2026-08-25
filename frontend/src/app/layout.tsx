import type { Metadata } from 'next'
import { Inter } from 'next/font/google'
import './globals.css'
import { Providers } from './providers'

const inter = Inter({ subsets: ['latin'] })

// The product is the memory layer, so the metadata leads with it. The
// previous copy described a governance control plane and never used the word
// "memory" — which meant the public front door advertised a different product
// from the one the repository is about, to humans and crawlers alike.
//
// Descriptions are written to be quotable in isolation: a model retrieving one
// of these sentences should be able to state what VIGIL MEMORY does, with a
// number, without needing the rest of the page.
const DESCRIPTION =
  'VIGIL MEMORY is a runtime firewall for autonomous AI agents that remembers which agents are banned across sessions and process restarts. Trust is a keyed database lookup — ~1ms in-process — not history replayed into a context window. No vectors, no embeddings, no LLM on the enforcement path.'

export const metadata: Metadata = {
  metadataBase: new URL('https://vigil-sibyl-memory.vercel.app'),
  title: 'VIGIL MEMORY — cross-session trust memory for AI agents',
  description: DESCRIPTION,
  keywords: [
    'AI agent security',
    'agent memory',
    'cross-session memory',
    'runtime firewall',
    'LLM security',
    'agent trust',
    'MCP',
    'Sibyl Memory',
    'Base',
    'Virtuals ACP',
  ],
  authors: [{ name: 'VIGIL MEMORY' }],
  openGraph: {
    title: 'VIGIL MEMORY — an agent you banned stays banned after a restart',
    description: DESCRIPTION,
    type: 'website',
    url: 'https://vigil-sibyl-memory.vercel.app',
    siteName: 'VIGIL MEMORY',
  },
  twitter: {
    card: 'summary_large_image',
    title: 'VIGIL MEMORY — an agent you banned stays banned after a restart',
    description: DESCRIPTION,
  },
  alternates: { canonical: '/' },
  robots: { index: true, follow: true },
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={`${inter.className} bg-white text-black min-h-screen antialiased`} suppressHydrationWarning>
        <Providers>
          {children}
        </Providers>
      </body>
    </html>
  )
}
