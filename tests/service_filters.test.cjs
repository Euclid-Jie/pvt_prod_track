const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const html = fs.readFileSync(path.join(__dirname, '../templates/service.html'), 'utf8');
const applyFilters = html.slice(html.indexOf('    function applyFilters()'), html.indexOf('    function updateExcessAvailability()'));

function filterFunds(excess, search = '') {
  const funds = [
    { manager: '国泰海通_私募标准指数', is_index: false, has_excess: true },
    { manager: '中证指数公司_中证1000', is_index: true, has_excess: true },
    { manager: '普通管理人', is_index: false, has_excess: true },
    { manager: '待补超额管理人', is_index: false, has_excess: false },
  ].map(fund => ({ strategy: '1000增强', scale: '-', ...fund }));
  const controls = {
    searchInput: { value: search },
    strategySelect: { value: '1000增强' },
    scaleSelect: { value: 'large' },
  };
  const context = vm.createContext({
    allFundsData: funds,
    el: id => controls[id],
    isExcessMode: () => excess,
    updateExcessAvailability() {},
    sortAndRender() {},
    updateStats() {},
  });
  vm.runInContext(`${applyFilters}\napplyFilters();`, context);
  return {
    ranking: Array.from(context.rankingData, fund => fund.manager),
    visible: Array.from(context.filteredData, fund => fund.manager),
  };
}

test('excess keeps private standard indices and excludes FOF99 indices', () => {
  const result = filterFunds(true, '指数');
  assert.deepEqual(result.visible, ['国泰海通_私募标准指数']);
  assert.deepEqual(result.ranking, ['国泰海通_私募标准指数', '普通管理人', '待补超额管理人']);
});

test('absolute mode retains both standard and benchmark indices', () => {
  const result = filterFunds(false, '指数');
  assert.deepEqual(result.visible, ['国泰海通_私募标准指数', '中证指数公司_中证1000']);
  assert.equal(result.ranking.length, 4);
});

test('embedded service page matches the source template', () => {
  assert.equal(fs.readFileSync(path.join(__dirname, '../go/assets/templates/service.html'), 'utf8'), html);
});
