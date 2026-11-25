/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Window } from 'happy-dom'

// Bun runs files in one process. Give each DOM test its own document and put
// back the exact globals left by the preceding test. Vitest provides jsdom.
export async function withReactTestDOM(run: () => Promise<void>) {
  const previous = new Map<string, PropertyDescriptor | undefined>()
  const browser = process.versions.bun ? new Window() : undefined
  function install(key: string, value: unknown) {
    previous.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
    Object.defineProperty(globalThis, key, {
      configurable: true,
      writable: true,
      value,
    })
  }
  try {
    if (browser) {
      for (const key of [
        'window',
        'document',
        'navigator',
        'HTMLElement',
        'HTMLInputElement',
        'SVGElement',
        'Node',
        'Element',
        'Event',
        'CustomEvent',
        'MutationObserver',
        'requestAnimationFrame',
        'cancelAnimationFrame',
        'getComputedStyle',
        'localStorage',
      ] as const) {
        install(key, browser[key])
      }
    }
    install('IS_REACT_ACT_ENVIRONMENT', true)
    await run()
  } finally {
    try {
      await browser?.close()
    } finally {
      for (const [key, descriptor] of previous) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor)
        else Reflect.deleteProperty(globalThis, key)
      }
    }
  }
}
