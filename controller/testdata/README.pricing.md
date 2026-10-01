# Official pricing fixtures

The CNY fixtures were retrieved on 2026-10-01 and reduced to the providers/models needed by the regression tests. They are historical test data, not runtime price overrides.

- `official_pricing_abacus_cny.json`: [LLM Abacus](https://www.llmabacus.com/api/prices), [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). The fixture retains original-currency fields; it omits unrelated models and metadata. The UI also links to LLM Abacus for attribution.
- `official_pricing_deepseek_cny.json`: [models-cn](https://null-object-0000.github.io/models-cn/v1/api.json), DeepSeek provider excerpt. Upstream [MIT license](https://github.com/null-object-0000/models-cn/blob/main/LICENSE) reproduced below.

## models-cn license

MIT License

Copyright (c) 2026 return null;

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
