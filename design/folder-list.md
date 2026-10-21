# 配置目录分区查询

## 接口约定

`POST /api/v1/folder/list` 复用现有分页响应 `{total, list}`，新增可选 `type`

| 参数 | 含义 |
| --- | --- |
| projectId | 查询项目一级目录时必填 |
| parentFolderId | 查询直接子目录，优先于 projectId，使用父目录实例 id |
| type | common 为通用目录，customer 为业务目录，不传查询全部类别 |
| code / name | 保留现有模糊筛选 |
| pageNum / pageSize | 保留现有分页规则，pageSize 最大 200 |

无效 type 返回现有业务错误，类别过滤同时应用于 total、分页候选和代表记录查询
不传 type 的调用保持兼容，数据库和 Java SDK 不需要变更

## 一级目录页面

1. 按 projectId 和 type=common 请求第一页，pageSize=200，当前通用一级目录仅 global / groups 两项
2. 通用目录单独显示在顶部，保持与业务目录相同卡片尺寸，窄屏自然换行
3. 等待 DOM 更新，扣除顶部区域实际高度和分区间距，再按剩余宽高计算业务目录分页大小，上限 200
4. 按 projectId 和 type=customer 查询业务目录，后续滚动和加载更多只请求这一类别
5. 业务分页 total 不包含通用目录，筛选和收藏继续作用于两类目录，无通用目录时不保留空行

查询通用目录失败时显示失败状态，不将其当作空目录继续加载
切换项目、刷新或搜索时重新加载两类目录，过期请求不更新当前状态
调整视口尺寸导致分页大小变化时，从第一页重新加载，避免分页边界混用

## groups 二级目录

进入 groups 继续调用同一 list 接口，传 parentFolderId，保留独立分页
二级查询不传 type，避免将类型为 common 的子目录按业务目录过滤掉
本次不调整二级目录布局，也不改变现有目录操作和权限逻辑
