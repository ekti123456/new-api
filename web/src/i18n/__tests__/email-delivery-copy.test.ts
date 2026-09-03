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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import en from '../locales/en.json'
import fr from '../locales/fr.json'
import ja from '../locales/ja.json'
import ru from '../locales/ru.json'
import vi from '../locales/vi.json'
import zhTW from '../locales/zh-TW.json'
import zh from '../locales/zh.json'

const emailDeliveryNotice =
  'If you have not received the email, please check your spam folder.'

describe('email delivery success copy', () => {
  test('includes the spam-folder reminder in every supported locale', () => {
    const locales = { en, fr, ja, ru, vi, zhTW, zh }

    for (const [locale, translations] of Object.entries(locales)) {
      assert.ok(
        translations.translation[emailDeliveryNotice],
        `${locale} is missing the email delivery reminder`
      )
    }

    assert.equal(
      en.translation[emailDeliveryNotice],
      'If you have not received the email, please check your spam folder.'
    )
    assert.equal(
      zh.translation[emailDeliveryNotice],
      '如果没有收到邮件，请检查垃圾邮件箱。'
    )
  })
})
