# 数据源与键名说明

本文档固定 `loadData()` 当前使用的三类基础产品来源，以及它们和 `Nav.nav_interval_metrics` 的关联规则。这里的规则短期内不要改成自动猜测或按 metrics 反推产品；基础产品是否展示必须以对应基础表的准入条件为准。

## 总体流程

`loadData()` 并发读取两类数据：

1. `Nav.nav_interval_metrics`：按 `fund_code` 做服务端 pivot，生成每个产品在各区间的指标列。
2. 基础产品信息：按顺序追加 `Euclid.fund_basic_info`、`Nav.PendingFund`、`Nav.fof99_nav_index`。

最终展示不是遍历 metrics 表，而是遍历基础产品信息。每条基础产品信息计算出一个 metric key，再到 pivot 结果中查找对应指标。非基准产品如果找不到 pivot 指标，会被过滤掉。

补充来源不去重。如果不同来源给出相同产品或相同 metric key，当前逻辑会按读取顺序各自进入后续流程；上游数据应负责避免不希望出现的重复。

## 统一字段

代码中用 `fundInfo` 承接三类来源，字段含义如下：

| `fundInfo` 字段 | 展示/关联含义 |
|---|---|
| `MetricCode` | 已经标准化后的 `Nav.nav_interval_metrics.fund_code`。补充来源直接填这个字段。 |
| `ProdCode` | 来源表产品代码，用于普通 Euclid 产品的 metric key。 |
| `ProdName` | 展示为产品名称。 |
| `ProdComp` | 展示为管理人。 |
| `ProdType` | 先映射 `strategyType`，再展示为策略类型。 |
| `Scale` | 展示为规模，并用于 `ScaleLevel`。 |
| `CompCode` | 管理人登记编号，仅用于补充来源补规模。 |
| `NavSource` | `Euclid.fund_basic_info.净值来源`，用于识别个人净值产品。 |
| `Fid` | `Euclid.fund_basic_info.fid`，用于个人净值产品 key。 |

## 来源一：Euclid.fund_basic_info

准入条件：

```sql
净值来源 IS NOT NULL
```

读取字段：

| 来源列 | `fundInfo` 字段 |
|---|---|
| `prod_code` | `ProdCode` |
| `prod_name` | `ProdName` |
| `prod_comp` | `ProdComp` |
| `prod_type` | `ProdType` |
| `管理人规模` | `Scale` |
| `净值来源` | `NavSource` |
| `fid` | `Fid` |

metric key 规则：

| 情况 | `Nav.nav_interval_metrics.fund_code` |
|---|---|
| `净值来源 = '个人净值'` 且 `fid` 有值 | `p_{fid}`，例如 `p_123` |
| 其他普通产品 | `prod_code` |

注意：`fund_basic_info.prod_code` 不能简单认为总是等于 `nav_interval_metrics.fund_code`，个人净值产品必须走 `p_{fid}`。

## 来源二：Nav.PendingFund

`PendingFund` 是待备案产品补充来源。准入必须以 `PendingFund` 自身为准，不能因为 `nav_interval_metrics` 里出现了 `pending:*` 指标就展示。

准入条件：

```sql
prod_comp IS NOT NULL AND TRIM(prod_comp) <> ''
```

读取字段：

| 来源列 | `fundInfo` 字段 |
|---|---|
| `PROD_CODE` | `ProdCode` |
| `PROD_NAME` | `ProdName` |
| `prod_comp` | `ProdComp` |
| `ProdType` | `ProdType` |
| `comp_code` | `CompCode` |

metric key 规则：

```text
pending:{PROD_CODE}
```

例子：`PROD_CODE = VU448B` 时，对应 `Nav.nav_interval_metrics.fund_code = pending:VU448B`。

规模补充：

`PendingFund` 本身没有规模字段。展示规模通过 `comp_code` 补：

```text
Nav.PendingFund.comp_code
  -> Euclid.量化私募管理人列表.登记编号
  -> Euclid.量化私募管理人列表.管理规模
```

如果 `comp_code` 找不到管理人规模，展示规模为 `-`，`scale_level` 按小厂处理。

## 来源三：Nav.fof99_nav_index

`fof99_nav_index` 是 FOF99 指数产品补充来源。当前逻辑直接追加，不做去重。

准入条件：

```sql
register_number IS NOT NULL
```

读取字段：

| 来源列 | `fundInfo` 字段 |
|---|---|
| `register_number` | `ProdCode` |
| `prod_name` | `ProdName` |
| `prod_comp` | `ProdComp` |
| `prod_type` | `ProdType` |
| `comp_code` | `CompCode` |

metric key 规则：

```text
fof99:{register_number}
```

例子：`register_number = SAHC27` 时，对应 `Nav.nav_interval_metrics.fund_code = fof99:SAHC27`。

规模补充：

`fof99_nav_index` 本身没有规模字段。展示规模通过 `comp_code` 补：

```text
Nav.fof99_nav_index.comp_code
  -> Euclid.量化私募管理人列表.登记编号
  -> Euclid.量化私募管理人列表.管理规模
```

## 指标表 pivot 规则

`Nav.nav_interval_metrics` 仍然必须使用服务端 pivot：

- `GROUP BY fund_code`
- 每个区间和指标用 `MAX(CASE WHEN ... THEN metric_value END)`
- `HAVING recent_week_return IS NOT NULL`

不要改回拉平后的 metrics 明细再在 Go 内存中 pivot。这个表的行数明显多于产品数，服务端 pivot 是当前性能设计。

## 展示过滤

遍历基础产品信息生成展示行时：

1. 先按来源规则得到 metric key。
2. 如果 `ProdComp != '基准'` 且 pivot 结果里没有这个 key，则跳过。
3. 如果 `ProdComp == '基准'`，允许没有指标，指标展示为 `-`。
4. `Scale == ''` 时展示为 `-`。
5. `Scale` 属于 `50-100亿元` 或 `100亿元以上` 时，`ScaleLevel = 大厂`；其他情况为 `小厂`。

## 修改时的检查清单

改任一数据源或键名时，至少检查：

1. 基础来源是否仍按自身准入条件控制展示。
2. metric key 是否和 `Nav.nav_interval_metrics.fund_code` 完全一致。
3. 补充来源是否仍通过 `comp_code -> 登记编号 -> 管理规模` 补规模。
4. 是否无意加入去重、覆盖、按 metrics 反推基础产品的逻辑。
5. `go test ./...` 是否通过。
