import { createFileRoute, lazyRouteComponent } from '@tanstack/react-router'

export const Route = createFileRoute('/_authenticated/windows/')({
  component: lazyRouteComponent(
    () => import('@/features/windows'),
    'PersonalWindows'
  ),
})
