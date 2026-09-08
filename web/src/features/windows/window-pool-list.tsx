import { useEffect, useState } from 'react'

import type { WindowPool } from './types'
import { WindowPoolCard } from './window-pool-card'

export function WindowPoolList(props: {
  pools: WindowPool[]
  offset: number
}): React.JSX.Element {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const update = () => {
      if (!document.hidden) setNow(Date.now())
    }
    const timer = setInterval(update, 1000)
    document.addEventListener('visibilitychange', update)
    return () => {
      clearInterval(timer)
      document.removeEventListener('visibilitychange', update)
    }
  }, [])
  return (
    <div className='space-y-4'>
      {props.pools.map((pool) => (
        <WindowPoolCard key={pool.id} pool={pool} now={now + props.offset} />
      ))}
    </div>
  )
}
