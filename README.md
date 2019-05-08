[![GoDoc](http://godoc.org/github.com/robfig/cron?status.png)](http://godoc.org/github.com/robfig/cron) 
[![Build Status](https://travis-ci.org/robfig/cron.svg?branch=master)](https://travis-ci.org/robfig/cron)

# cron
1、 增加remove任务的功能 (注意：原作者在V3版本中增加类似功能，可以实现删除任务)
```go
c := server.cron
c.AddNameFunc("cronName","0 0 8 * * ?", func() {
    // do something
})
c.Start()
c.RemoveJob("cronName")
```
2、 增加一个随机延迟的功能，添加定时任务时传入一个延时最大秒数`delayRange`，每次执行任务时会随机延时n秒，最大不超过传入的`delayRange`秒 .
```go
c := server.cron
c.AddDelayFunc("0 0 8 * * ?",1800, func() {
    // do something
})
c.Start()
```
> 上面样例`0 0 8 * * ?`表示每天8:00开始执行，`1800秒=30分钟` ， 所以最终执行时间为`8:00-8:30`之间，每次执行时间都不固定，但是范围是确定的(8:00-8:30)。
传入的`delayRange`参数最大不超过`82800秒（24小时）`

--------

> 我自己主要是使用延迟功能，比如说你写了个爬虫或者什么薅羊毛的定时服务，每天都是准时准秒的去拉别人数据感觉不合适，所以使用随机延迟的话就合理多了。
 
参考 https://github.com/jakecoffman/cron
因为`jakecoffman/cron`很久没更新（等待V3版本正式发布），拉取原仓库`robfig/cron`进行了更新
参考以下文章优化了`jakecoffman/cron`中删除切片元素
 - [slice删除元素的性能对比](https://www.jianshu.com/p/d276aa7300d1)
 - [golang删除slice中特定条件的元素，优化版](https://blog.csdn.net/liyunlong41/article/details/85132603)

Documentation here: https://godoc.org/github.com/robfig/cron
