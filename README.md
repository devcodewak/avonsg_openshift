### gsnova 0.31.0 修改版，用于openshift v3 docker部署  

  
##### 修改项  
`
基准：0.31.0 commit 0e3e110 jan 1,2018  
目标：尝试规避openshift代理检查封号等  
1 随机证书改为2048bit(1024)  
2 server端默认Mux.IdleTimeout改为1800(300)，即使无数据也保持长连接30分钟  
3 仅在client模式时显示ASCIILogo，server模式时不显示  
4 remote.indexCallback改为http.StatusOK,即去掉原有https访问时的版本提示，规避检查需要  
5 加大默认的Mux的MaxStreamWindow和StreamMinRefresh为原有4倍，即2048k和128k  
6 client模式时，显示心跳包延迟时间  
7 版本号r16_v31_M23G1  
8 增加key的环境变量AVONSG_CIPHER_KEY，也保留原有GSNOVA_CIPHER_KEY，规避检查需要  
9 加入logger.Printf，修改所有包log.Printf为logger调用  
10 修改logger包，加入none及null选项，便于server端命令行模式时，使用-log none关闭所有提示  
`
  
  
  
##### thanks yinqiwen :  
	https://github.com/yinqiwen/gsnova  
  
  
  
